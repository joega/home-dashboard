package media

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

const castConnection = "urn:x-cast:com.google.cast.tp.connection"
const castReceiver = "urn:x-cast:com.google.cast.receiver"
const castMedia = "urn:x-cast:com.google.cast.media"
const castHeartbeat = "urn:x-cast:com.google.cast.tp.heartbeat"

// These LAN protocols use device-issued certificates rather than public CAs.
// Pin the first server public key; later connections reject a changed key before
// sending any stored token. This exception is confined to TV protocol sockets.
func (c *Client) deviceTLS(ctx context.Context, address, pin string) (net.Conn, string, error) {
	raw, e := c.Dial(ctx, "tcp", address)
	if e != nil {
		return nil, "", e
	}
	fingerprint := ""
	conn := tls.Client(raw, &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("Missing TV certificate")
		}
		h := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
		fingerprint = hex.EncodeToString(h[:])
		if pin != "" && pin != fingerprint {
			return errors.New("TV certificate changed; forget pairing and verify the device")
		}
		return nil
	}})
	if e = conn.HandshakeContext(ctx); e != nil {
		raw.Close()
		return nil, "", e
	}
	deadline := time.Now().Add(4 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	conn.SetDeadline(deadline)
	return conn, fingerprint, nil
}
func fieldString(dst []byte, field byte, s string) []byte {
	dst = append(dst, field<<3|2)
	dst = binary.AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}
func encodeCast(namespace, destination string, payload any) []byte {
	body, _ := json.Marshal(payload)
	b := []byte{8, 0}
	b = fieldString(b, 2, "home-hq")
	b = fieldString(b, 3, destination)
	b = fieldString(b, 4, namespace)
	b = append(b, 40, 0)
	b = fieldString(b, 6, string(body))
	return b
}
func decodeCast(b []byte) (string, []byte, error) {
	var namespace string
	var payload []byte
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return "", nil, errors.New("Invalid Cast field")
		}
		b = b[n:]
		switch tag & 7 {
		case 0:
			_, n = binary.Uvarint(b)
			if n <= 0 {
				return "", nil, errors.New("Invalid Cast varint")
			}
			b = b[n:]
		case 2:
			length, n := binary.Uvarint(b)
			if n <= 0 || length > uint64(len(b)-n) {
				return "", nil, errors.New("Invalid Cast length")
			}
			v := b[n : n+int(length)]
			b = b[n+int(length):]
			if tag>>3 == 4 {
				namespace = string(v)
			}
			if tag>>3 == 6 {
				payload = append([]byte(nil), v...)
			}
		case 1:
			if len(b) < 8 {
				return "", nil, io.ErrUnexpectedEOF
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return "", nil, io.ErrUnexpectedEOF
			}
			b = b[4:]
		default:
			return "", nil, errors.New("Unsupported Cast wire field")
		}
	}
	if namespace == "" || payload == nil {
		return "", nil, errors.New("Missing Cast envelope")
	}
	return namespace, payload, nil
}
func sendCast(conn net.Conn, namespace, destination string, payload any) error {
	b := encodeCast(namespace, destination, payload)
	packet := make([]byte, 4, len(b)+4)
	binary.BigEndian.PutUint32(packet, uint32(len(b)))
	packet = append(packet, b...)
	for len(packet) > 0 {
		n, e := conn.Write(packet)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}
func receiveCast(conn net.Conn) (string, []byte, error) {
	header := make([]byte, 4)
	if _, e := io.ReadFull(conn, header); e != nil {
		return "", nil, e
	}
	n := binary.BigEndian.Uint32(header)
	if n == 0 || n > 65536 {
		return "", nil, errors.New("Oversized Cast message")
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(conn, b); e != nil {
		return "", nil, e
	}
	return decodeCast(b)
}
func (c *Client) readCast(ctx context.Context, d Device, pin string) (Snapshot, error) {
	conn, key, e := c.deviceTLS(ctx, net.JoinHostPort(d.Host, "8009"), pin)
	if e != nil {
		return Snapshot{}, e
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if e = sendCast(conn, castConnection, "receiver-0", map[string]any{"type": "CONNECT", "origin": map[string]any{}}); e != nil {
		return Snapshot{}, e
	}
	if e = sendCast(conn, castReceiver, "receiver-0", map[string]any{"type": "GET_STATUS", "requestId": 1}); e != nil {
		return Snapshot{}, e
	}
	status := Snapshot{Reachable: true, CastPin: key}
	receiverSeen := false
	mediaAsked := false
	for count := 0; count < 24; count++ {
		ns, b, e := receiveCast(conn)
		if e != nil {
			if receiverSeen {
				break
			}
			return Snapshot{}, e
		}
		var kind struct{ Type string }
		if json.Unmarshal(b, &kind) != nil {
			continue
		}
		if ns == castHeartbeat && kind.Type == "PING" {
			sendCast(conn, castHeartbeat, "receiver-0", map[string]string{"type": "PONG"})
			continue
		}
		if ns == castReceiver && kind.Type == "RECEIVER_STATUS" {
			var result struct {
				Status struct {
					Applications []struct {
						DisplayName, TransportID string
						Namespaces               []struct{ Name string }
					}
					Volume struct {
						Level *float64
						Muted *bool
					}
				}
			}
			if json.Unmarshal(b, &result) != nil {
				continue
			}
			receiverSeen = true
			if result.Status.Volume.Level != nil {
				v := int(*result.Status.Volume.Level*100 + .5)
				if v >= 0 && v <= 100 {
					status.Volume = &v
					status.Muted = result.Status.Volume.Muted
				}
			}
			for _, app := range result.Status.Applications {
				status.App = Text(app.DisplayName, 120)
				for _, ns := range app.Namespaces {
					if ns.Name == castMedia && app.TransportID != "" {
						sendCast(conn, castConnection, app.TransportID, map[string]any{"type": "CONNECT", "origin": map[string]any{}})
						sendCast(conn, castMedia, app.TransportID, map[string]any{"type": "GET_STATUS", "requestId": 2})
						mediaAsked = true
						break
					}
				}
				if mediaAsked {
					break
				}
			}
			if !mediaAsked {
				break
			}
		}
		if ns == castMedia && kind.Type == "MEDIA_STATUS" {
			var result struct {
				Status []struct {
					PlayerState string
					CurrentTime float64
					Media       struct {
						Duration float64
						Metadata struct{ Title, Artist, AlbumName, SeriesTitle, EpisodeTitle string }
					}
				}
			}
			if json.Unmarshal(b, &result) == nil && len(result.Status) > 0 {
				v := result.Status[0]
				status.Playback = Text(v.PlayerState, 30)
				status.Title = Text(v.Media.Metadata.Title, 200)
				if status.Title == "" {
					status.Title = Text(v.Media.Metadata.EpisodeTitle, 200)
				}
				status.Artist = Text(v.Media.Metadata.Artist, 160)
				if status.Artist == "" {
					status.Artist = Text(v.Media.Metadata.SeriesTitle, 160)
				}
				status.Album = Text(v.Media.Metadata.AlbumName, 160)
				status.Position = v.CurrentTime
				status.Duration = v.Media.Duration
			}
			break
		}
	}
	if status.Title == "" {
		if status.App == "" {
			status.Note = "No active Cast session is reported. This does not establish TV power."
		} else {
			status.Note = "The app is not exposing playback details through Cast."
		}
	}
	return status, status.Validate()
}
