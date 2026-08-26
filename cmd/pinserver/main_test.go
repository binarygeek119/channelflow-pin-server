package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/binarygeek119/channelflow-pin/internal/pins"
	webui "github.com/binarygeek119/channelflow-pin/web"
)

func testMux(h *pins.Hub) http.Handler {
	s := &server{hub: h, web: http.FileServer(http.FS(webui.FS))}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/wait", s.handleWait)
	mux.HandleFunc("/v1/pins/", s.handlePins)
	mux.HandleFunc("/v1/status", s.handleStatus)
	mux.HandleFunc("/", s.handleStatic)
	return withCORS(mux)
}

func TestHealthAndDeliver(t *testing.T) {
	h := pins.NewHub()
	srv := httptest.NewServer(testMux(h))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("health %d", res.StatusCode)
	}

	res, err = http.Post(srv.URL+"/v1/wait", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var issued struct {
		Pin string `json:"pin"`
	}
	if err := json.NewDecoder(res.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(issued.Pin) != 8 {
		t.Fatalf("pin %q", issued.Pin)
	}

	body, _ := json.Marshal(map[string]string{"ciphertext": "Y2lwaGVy"})
	res, err = http.Post(srv.URL+"/v1/pins/"+issued.Pin+"/deliver", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("deliver %d", res.StatusCode)
	}

	res, err = http.Post(srv.URL+"/v1/pins/ZZZZZZZZ/deliver", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown %d", res.StatusCode)
	}
}

func TestBrandAssets(t *testing.T) {
	h := pins.NewHub()
	srv := httptest.NewServer(testMux(h))
	defer srv.Close()

	for _, path := range []string{
		"/favicon.ico",
		"/favicon-16x16.png",
		"/favicon-32x32.png",
		"/apple-touch-icon.png",
		"/android-chrome-192x192.png",
		"/android-chrome-512x512.png",
		"/logo.png",
		"/logo-plane.png",
		"/site.webmanifest",
	} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
}
