package dashboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"image"
	imgcolor "image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Imports are public documents, never a route into the household LAN or file system.
func recipeURL(raw string) (*url.URL, error) {
	if len(raw) > 2000 {
		return nil, errors.New("URL too long")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("Use a public HTTP or HTTPS URL.")
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return nil, errors.New("Only public web ports are supported.")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, errors.New("Local addresses cannot be imported.")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicRecipeIP(ip) {
		return nil, errors.New("Local/private addresses cannot be imported.")
	}
	u.Fragment = ""
	return u, nil
}

var blockedRecipeRanges = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32")}

func publicRecipeIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blockedRecipeRanges {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func recipeHTTPClient() *http.Client {
	tr := &http.Transport{Proxy: nil, MaxIdleConns: 16, MaxConnsPerHost: 6, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("No public address found")
		}
		for _, ip := range ips {
			if !publicRecipeIP(ip) {
				return nil, errors.New("Private network destinations are blocked")
			}
		}
		var last error
		for _, ip := range ips {
			c, e := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: tr, Timeout: 12 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("Too many redirects")
		}
		_, err := recipeURL(r.URL.String())
		return err
	}}
}
func fetchRecipeBytes(ctx context.Context, c *http.Client, raw string, limit int64) ([]byte, *url.URL, error) {
	u, err := recipeURL(raw)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "HomeHQ-RecipeImporter/1.0")
	req.Header.Set("Accept", "text/html,application/ld+json,application/json,image/jpeg,image/png")
	res, err := c.Do(req)
	if err != nil {
		return nil, nil, errors.New("Could not fetch that public page. Try a recipe file or manual entry.")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, nil, fmt.Errorf("The source returned HTTP %d. Try manual entry or another link.", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(b)) > limit {
		return nil, nil, errors.New("The source document is too large")
	}
	return b, res.Request.URL, nil
}
func plainRecipeText(v string) string {
	z := html.NewTokenizer(strings.NewReader(v))
	var out strings.Builder
	skip := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		t := z.Token()
		if tt == html.StartTagToken {
			if t.Data == "script" || t.Data == "style" {
				skip++
			}
			if t.Data == "br" || t.Data == "p" || t.Data == "li" {
				out.WriteByte('\n')
			}
		}
		if tt == html.EndTagToken && (t.Data == "script" || t.Data == "style") && skip > 0 {
			skip--
		}
		if tt == html.TextToken && skip == 0 {
			out.WriteString(t.Data)
		}
	}
	return strings.TrimSpace(out.String())
}
func jsonText(v any) string {
	switch x := v.(type) {
	case string:
		return plainRecipeText(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case map[string]any:
		if s := jsonText(x["name"]); s != "" {
			return s
		}
		return jsonText(x["value"])
	case []any:
		var a []string
		for _, v := range x {
			if s := jsonText(v); s != "" {
				a = append(a, s)
			}
		}
		return strings.Join(a, ", ")
	}
	return ""
}

var stepNumber = regexp.MustCompile(`(?i)^\s*(?:step\s+)?\d+[.):]\s+`)

var stepHeading = regexp.MustCompile(`(?i)^\s*step\s+\d+[.):]?\s*$`)

func recipeSteps(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		for _, s := range strings.Split(plainRecipeText(x), "\n") {
			if stepHeading.MatchString(s) {
				continue
			}
			if s = strings.TrimSpace(stepNumber.ReplaceAllString(s, "")); s != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, v := range x {
			out = append(out, recipeSteps(v)...)
		}
	case map[string]any:
		if elems, ok := x["itemListElement"]; ok {
			out = append(out, recipeSteps(elems)...)
		} else if text := jsonText(x["text"]); text != "" {
			out = recipeSteps(text)
		} else if name := jsonText(x["name"]); name != "" {
			out = []string{name}
		}
	}
	return out
}

var isoRecipeDuration = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func recipeMinutes(v any) int {
	m := isoRecipeDuration.FindStringSubmatch(jsonText(v))
	if m == nil {
		return 0
	}
	n := 0
	factors := []int{1440, 60, 1, 0}
	for i, f := range factors {
		x, _ := strconv.Atoi(m[i+1])
		n += x * f
	}
	if n > 10080 {
		return 0
	}
	return n
}
func imageReference(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		for _, v := range x {
			if s := imageReference(v); s != "" {
				return s
			}
		}
	case map[string]any:
		if s := imageReference(x["url"]); s != "" {
			return s
		}
		return imageReference(x["contentUrl"])
	}
	return ""
}
func fromSchemaRecipe(m map[string]any, source string) Recipe {
	r := Recipe{Title: jsonText(m["name"]), Description: jsonText(m["description"]), Meal: "Dinner", Servings: jsonText(m["recipeYield"]), PrepMinutes: recipeMinutes(m["prepTime"]), CookMinutes: recipeMinutes(m["cookTime"]), TotalMinutes: recipeMinutes(m["totalTime"]), Steps: recipeSteps(m["recipeInstructions"]), Author: jsonText(m["author"]), Source: source, Notes: jsonText(m["householdNotes"]), PhotoURL: imageReference(m["image"]), Active: true}
	switch x := m["recipeIngredient"].(type) {
	case []any:
		for _, v := range x {
			r.Ingredients = append(r.Ingredients, jsonText(v))
		}
	case string:
		r.Ingredients = strings.Split(plainRecipeText(x), "\n")
	case map[string]any:
		if list, ok := x["itemListElement"].([]any); ok {
			for _, v := range list {
				r.Ingredients = append(r.Ingredients, jsonText(v))
			}
		}
	}
	category := strings.ToLower(jsonText(m["recipeCategory"]))
	for _, meal := range recipeMeals {
		if strings.Contains(category, strings.ToLower(meal)) {
			r.Meal = meal
			break
		}
	}
	if r.Source == "" {
		r.Source = jsonText(m["url"])
	}
	r.Tags = strings.Split(jsonText(m["keywords"]), ",")
	if cuisine := jsonText(m["recipeCuisine"]); cuisine != "" {
		r.Tags = append(r.Tags, cuisine)
	}
	if r.PhotoURL != "" && !strings.HasPrefix(r.PhotoURL, "data:") {
		if base, e := url.Parse(source); e == nil {
			if rel, e := url.Parse(r.PhotoURL); e == nil {
				r.PhotoURL = base.ResolveReference(rel).String()
			}
		}
	}
	r.normalize()
	return r
}
func schemaRecipes(raw []byte, source string) ([]Recipe, error) {
	var documents []any
	var obj any
	if json.Unmarshal(raw, &obj) == nil {
		documents = append(documents, obj)
	} else {
		z := html.NewTokenizer(bytes.NewReader(raw))
		z.SetMaxBuf(2 << 20)
		reading := false
		var script strings.Builder
		for {
			tt := z.Next()
			if tt == html.ErrorToken {
				break
			}
			t := z.Token()
			if tt == html.StartTagToken && t.Data == "script" {
				reading = false
				script.Reset()
				for _, a := range t.Attr {
					if a.Key == "type" && strings.EqualFold(strings.TrimSpace(a.Val), "application/ld+json") {
						reading = true
					}
				}
			}
			if tt == html.TextToken && reading {
				script.WriteString(t.Data)
			}
			if tt == html.EndTagToken && t.Data == "script" && reading {
				var doc any
				if json.Unmarshal([]byte(script.String()), &doc) == nil {
					documents = append(documents, doc)
				}
				reading = false
			}
		}
	}
	var found []Recipe
	var walk func(any, int)
	walk = func(v any, depth int) {
		if depth > 40 || len(found) >= 10 {
			return
		}
		switch x := v.(type) {
		case []any:
			for _, v := range x {
				walk(v, depth+1)
			}
		case map[string]any:
			kind := jsonText(x["@type"])
			if strings.Contains(","+strings.ReplaceAll(kind, " ", "")+",", ",Recipe,") || kind == "https://schema.org/Recipe" || kind == "http://schema.org/Recipe" {
				r := fromSchemaRecipe(x, source)
				if r.Title != "" {
					found = append(found, r)
				}
				return
			}
			for _, key := range []string{"@graph", "mainEntity", "itemListElement", "item", "hasPart"} {
				if value, ok := x[key]; ok {
					walk(value, depth+1)
				}
			}
		}
	}
	for _, doc := range documents {
		walk(doc, 0)
	}
	if len(found) == 0 {
		return nil, errors.New("No usable Recipe metadata found. You can add this recipe manually or import a JSON-LD file.")
	}
	return found, nil
}
func recipePhoto(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, (3<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 3<<20 {
		return nil, errors.New("Choose a photo smaller than 3 MB.")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errors.New("Choose a JPG or PNG photo.")
	}
	if cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return nil, errors.New("Choose a photo under 16 megapixels.")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("The photo could not be decoded")
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > 640 {
		h = max(1, h*640/w)
		w = 640
	}
	if h > 640 {
		w = max(1, w*640/h)
		h = 640
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, blue, a := src.At(b.Min.X+x*b.Dx()/w, b.Min.Y+y*b.Dy()/h).RGBA()
			dst.SetRGBA(x, y, imgcolor.RGBA{uint8((r + 65535 - a) >> 8), uint8((g + 65535 - a) >> 8), uint8((blue + 65535 - a) >> 8), 255})
		}
	}
	var out bytes.Buffer
	err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82})
	return out.Bytes(), err
}
func loadRecipePhoto(ctx context.Context, c *http.Client, raw string) ([]byte, error) {
	if strings.HasPrefix(raw, "data:") {
		parts := strings.SplitN(raw, ",", 2)
		if len(parts) != 2 || (parts[0] != "data:image/jpeg;base64" && parts[0] != "data:image/png;base64") {
			return nil, errors.New("Unsupported photo data")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, err
		}
		return recipePhoto(bytes.NewReader(data))
	}
	b, _, err := fetchRecipeBytes(ctx, c, raw, 3<<20)
	if err != nil {
		return nil, err
	}
	return recipePhoto(bytes.NewReader(b))
}
