package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthProbeAddress(t *testing.T) {
	for address, want := range map[string]string{
		"0.0.0.0:4173":   "http://127.0.0.1:4173/healthz",
		":4173":          "http://127.0.0.1:4173/healthz",
		"[::]:4173":      "http://[::1]:4173/healthz",
		"127.0.0.1:4181": "http://127.0.0.1:4181/healthz",
	} {
		got, err := healthURL(address)
		if err != nil || got != want {
			t.Fatal(address, got, err)
		}
	}
	if _, err := healthURL("bad address"); err == nil {
		t.Fatal("invalid address accepted")
	}
}

func TestHealthProbeRejectsFailuresAndRedirects(t *testing.T) {
	for _, test := range []struct {
		status int
		body   string
		wantOK bool
	}{
		{200, "ok\n", true}, {503, "Unavailable", false}, {200, "not a health check", false}, {302, "", false},
	} {
		t.Run(http.StatusText(test.status)+test.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Error("wrong probe path", r.URL.Path)
				}
				if test.status == 302 {
					w.Header().Set("Location", "/healthz")
				}
				w.WriteHeader(test.status)
				w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := checkHealth(strings.TrimPrefix(server.URL, "http://"))
			if (err == nil) != test.wantOK {
				t.Fatal(err)
			}
		})
	}
	server := httptest.NewServer(http.NotFoundHandler())
	address := strings.TrimPrefix(server.URL, "http://")
	server.Close()
	if checkHealth(address) == nil {
		t.Fatal("unavailable server accepted")
	}
}
