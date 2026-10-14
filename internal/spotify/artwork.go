package spotify

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
)

func (c *Client) Artwork(ctx context.Context, address string) ([]byte, string, error) {
	if !AllowedImage(address) {
		return nil, "", errors.New("Unapproved Spotify artwork host")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return nil, "", errors.New("Artwork unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", errors.New("Artwork unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if e != nil || len(b) > 4*1024*1024 {
		return nil, "", errors.New("Artwork too large")
	}
	cfg, kind, e := image.DecodeConfig(bytes.NewReader(b))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || (kind != "jpeg" && kind != "png") {
		return nil, "", errors.New("Invalid artwork")
	}
	mime := "image/png"
	if kind == "jpeg" {
		mime = "image/jpeg"
	}
	return b, mime, nil
}
