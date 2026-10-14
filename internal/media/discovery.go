package media

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type Discoverer interface {
	Discover(context.Context) ([]Device, error)
}
type LAN struct{ Client *Client }

func (l LAN) Discover(ctx context.Context) ([]Device, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var devices []Device
	var errs []error
	add := func(ds []Device, err error) {
		mu.Lock()
		defer mu.Unlock()
		devices = append(devices, ds...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	wg.Add(2)
	go func() { defer wg.Done(); ds, e := discoverSSDP(ctx, l.Client); add(ds, e) }()
	go func() { defer wg.Done(); ds, e := discoverMDNS(ctx); add(ds, e) }()
	wg.Wait()
	// One physical device can advertise both Sony UPnP and Cast. Prefer its
	// Cast identity when present: that remains available while the TV is off.
	byHost := map[string]Device{}
	rank := func(d Device) int {
		if d.Cast {
			return 2
		}
		if strings.HasPrefix(d.UID, "upnp:") {
			return 1
		}
		return 0
	}
	sort.SliceStable(devices, func(i, j int) bool {
		if rank(devices[i]) != rank(devices[j]) {
			return rank(devices[i]) < rank(devices[j])
		}
		return devices[i].UID < devices[j].UID
	})
	for _, d := range devices {
		old, ok := byHost[d.Host]
		if ok {
			if old.Kind != "cast" && d.Kind == "cast" {
				d.Kind = old.Kind
			}
			if d.Model == "" {
				d.Model = old.Model
			}
			d.Cast = d.Cast || old.Cast
			d.Aliases = IdentityAliases(d, old)
		}
		byHost[d.Host] = d
	}
	out := []Device{}
	for _, d := range byHost {
		if d.Validate() == nil {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > MaxDevices {
		out = out[:MaxDevices]
	}
	if len(out) == 0 && len(errs) > 0 {
		return out, errors.New("LAN discovery unavailable; add a device by its private IPv4 address")
	}
	return out, nil
}

func discoverSSDP(ctx context.Context, c *Client) ([]Device, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	destination := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	for _, target := range []string{"urn:schemas-upnp-org:device:MediaRenderer:1", "urn:dial-multiscreen-org:service:dial:1", "urn:samsung.com:device:RemoteControlReceiver:1", "urn:samsung.com:service:MainTVAgent2:1", "urn:schemas-upnp-org:service:RenderingControl:1"} {
		query := "M-SEARCH * HTTP/1.1\r\nHOST:239.255.255.250:1900\r\nMAN:\"ssdp:discover\"\r\nMX:1\r\nST:" + target + "\r\n\r\n"
		if _, err = conn.WriteToUDP([]byte(query), destination); err != nil {
			return nil, err
		}
	}
	locations := map[string]string{}
	buf := make([]byte, 8192)
	for i := 0; i < 128; i++ {
		n, source, e := conn.ReadFromUDP(buf)
		if e != nil {
			break
		}
		host := source.IP.String()
		if !PrivateHost(host) {
			continue
		}
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			k, v, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(strings.TrimSpace(k), "location") {
				u, e := url.Parse(strings.TrimSpace(v))
				if e == nil && u.Scheme == "http" && u.User == nil && u.Hostname() == host && u.Fragment == "" && len(locations) < 32 {
					locations[u.String()] = host
				}
			}
		}
	}
	out := []Device{}
	for address, host := range locations {
		if ctx.Err() != nil {
			break
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
		// Only device-description GETs at the advertising source; never follow redirects.
		resp, e := c.HTTP.Do(req)
		if e != nil {
			continue
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, 128*1024+1))
		resp.Body.Close()
		if e != nil || resp.StatusCode != 200 || len(b) > 128*1024 {
			continue
		}
		d, e := parseDescription(b, host)
		if e == nil {
			out = append(out, d)
		}
	}
	return out, nil
}
func parseDescription(b []byte, host string) (Device, error) {
	var root struct {
		Device struct {
			Manufacturer string `xml:"manufacturer"`
			FriendlyName string `xml:"friendlyName"`
			ModelName    string `xml:"modelName"`
			UDN          string `xml:"UDN"`
		} `xml:"device"`
	}
	if err := xml.Unmarshal(b, &root); err != nil {
		return Device{}, err
	}
	v := root.Device
	kind := ""
	manufacturer := strings.ToLower(v.Manufacturer)
	if strings.Contains(manufacturer, "sony") {
		kind = "sony"
	}
	if strings.Contains(manufacturer, "samsung") {
		kind = "samsung"
	}
	if kind == "" || v.UDN == "" {
		return Device{}, errors.New("Not a supported TV description")
	}
	d := Device{Host: host, Kind: kind, Name: Text(v.FriendlyName, 100), Model: Text(v.ModelName, 100), UID: "upnp:" + Text(v.UDN, 160), Enabled: true, Seen: time.Now()}
	d.ID = ID(d.UID)
	return d, d.Validate()
}

func discoverMDNS(ctx context.Context) ([]Device, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	out := []Device{}
	listeners := 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		local := false
		for _, a := range addresses {
			ip, _, e := net.ParseCIDR(a.String())
			if e == nil && ip.To4() != nil && PrivateHost(ip.String()) {
				local = true
			}
		}
		if !local || listeners >= 4 {
			continue
		}
		listeners++
		conn, e := net.ListenMulticastUDP("udp4", &iface, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
		if e != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			ds := mdnsQuery(ctx, conn)
			mu.Lock()
			out = append(out, ds...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if listeners == 0 {
		return nil, errors.New("No multicast LAN interface")
	}
	return out, nil
}
func mdnsQuery(ctx context.Context, conn *net.UDPConn) []Device {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	name, _ := dnsmessage.NewName("_googlecast._tcp.local.")
	airplay, _ := dnsmessage.NewName("_airplay._tcp.local.")
	query, _ := (&dnsmessage.Message{Header: dnsmessage.Header{}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}, {Name: airplay, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}}}).Pack()
	conn.WriteToUDP(query, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
	texts := map[string][]string{}
	names := map[string]string{}
	targets := map[string]string{}
	addresses := map[string]string{}
	buf := make([]byte, 65535)
	for i := 0; i < 256; i++ {
		n, source, e := conn.ReadFromUDP(buf)
		if e != nil {
			break
		}
		if !PrivateHost(source.IP.String()) {
			continue
		}
		var m dnsmessage.Message
		if m.Unpack(buf[:n]) != nil {
			continue
		}
		for _, r := range append(append(m.Answers, m.Authorities...), m.Additionals...) {
			key := strings.ToLower(r.Header.Name.String())
			switch b := r.Body.(type) {
			case *dnsmessage.TXTResource:
				if strings.HasSuffix(key, "._googlecast._tcp.local.") || strings.HasSuffix(key, "._airplay._tcp.local.") {
					texts[key] = b.TXT
					names[key] = r.Header.Name.String()
				}
			case *dnsmessage.SRVResource:
				if b.Port == 8009 || strings.HasSuffix(key, "._airplay._tcp.local.") {
					targets[key] = strings.ToLower(b.Target.String())
				}
			case *dnsmessage.AResource:
				host := net.IP(b.A[:]).String()
				if PrivateHost(host) {
					addresses[key] = host
				}
			}
		}
		if len(texts) > 64 || len(targets) > 256 || len(addresses) > 512 {
			break
		}
	}
	out := []Device{}
	for service, txt := range texts {
		host := addresses[targets[service]]
		if !PrivateHost(host) {
			continue
		}
		values := map[string]string{}
		for _, s := range txt {
			k, v, ok := strings.Cut(s, "=")
			if ok {
				values[k] = v
			}
		}
		if strings.HasSuffix(service, "._airplay._tcp.local.") {
			brand := strings.ToLower(values["manufacturer"])
			kind := ""
			if strings.Contains(brand, "samsung") {
				kind = "samsung"
			}
			if strings.Contains(brand, "sony") {
				kind = "sony"
			}
			if kind == "" || values["deviceid"] == "" {
				continue
			}
			name := strings.TrimSuffix(names[service], "._airplay._tcp.local.")
			d := Device{Host: host, Kind: kind, Name: Text(name, 80), Model: Text(values["model"], 80), UID: "airplay:" + values["deviceid"], Enabled: true, Seen: time.Now()}
			d.ID = ID(d.UID)
			if d.Validate() == nil {
				out = append(out, d)
			}
			continue
		}
		if values["id"] == "" || len(values["id"]) > 120 {
			continue
		}
		kind := "cast"
		if strings.Contains(strings.ToUpper(values["md"]), "BRAVIA") {
			kind = "sony"
		}
		d := Device{Host: host, Kind: kind, Name: Text(values["fn"], 100), Model: Text(values["md"], 100), UID: "cast:" + values["id"], Cast: true, Enabled: true, Seen: time.Now()}
		d.ID = ID(d.UID)
		if d.Validate() == nil {
			out = append(out, d)
		}
	}
	return out
}
