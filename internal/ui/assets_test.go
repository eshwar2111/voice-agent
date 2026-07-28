package ui

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAssetServerServesIndex(t *testing.T) {
	srv, err := startAssetServer()
	if err != nil {
		t.Fatalf("startAssetServer: %v", err)
	}
	defer srv.Close()

	if !strings.HasSuffix(srv.URL, "/") {
		t.Fatalf("URL %q must end in /", srv.URL)
	}
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		t.Fatalf("URL %q must be loopback", srv.URL)
	}

	resp, err := http.Get(srv.URL + "index.html")
	if err != nil {
		t.Fatalf("GET index.html: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("index.html status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<!DOCTYPE html>") {
		t.Errorf("index.html does not look like the overlay page")
	}
}

func TestAssetServerRejectsUnprefixedPaths(t *testing.T) {
	srv, err := startAssetServer()
	if err != nil {
		t.Fatalf("startAssetServer: %v", err)
	}
	defer srv.Close()

	// Strip the random prefix — a caller guessing the port must still miss.
	base := srv.URL[:strings.Index(srv.URL[len("http://"):], "/")+len("http://")+1]
	resp, err := http.Get(base + "index.html")
	if err != nil {
		t.Fatalf("GET unprefixed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Errorf("unprefixed path returned 200, want 404")
	}
}

func TestAssetServerPrefixIsRandomPerLaunch(t *testing.T) {
	a, err := startAssetServer()
	if err != nil {
		t.Fatalf("startAssetServer: %v", err)
	}
	defer a.Close()
	b, err := startAssetServer()
	if err != nil {
		t.Fatalf("startAssetServer: %v", err)
	}
	defer b.Close()
	if a.URL == b.URL {
		t.Errorf("two servers produced the same URL %q", a.URL)
	}
}
