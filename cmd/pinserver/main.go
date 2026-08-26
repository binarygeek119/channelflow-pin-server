package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/acme/autocert"

	"github.com/binarygeek119/channelflow-pin/internal/duckdns"
	"github.com/binarygeek119/channelflow-pin/internal/pins"
	"github.com/binarygeek119/channelflow-pin/internal/quickpin"
	webui "github.com/binarygeek119/channelflow-pin/web"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

type server struct {
	hub *pins.Hub
	web http.Handler
}

type deliverBody struct {
	Ciphertext string `json:"ciphertext"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run() error {
	s := &server{
		hub: pins.NewHub(),
		web: http.FileServer(http.FS(webui.FS)),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/wait", s.handleWait)
	mux.HandleFunc("/v1/pins/", s.handlePins)
	mux.HandleFunc("/v1/status", s.handleStatus)
	mux.HandleFunc("/", s.handleStatic)
	h := withCORS(mux)

	if envTruthy("RELAY_DEV") {
		httpSrv := newHTTPServer(listenAddr(), h)
		return serveUntilSignal(httpSrv)
	}

	sub := os.Getenv("DUCKDNS_SUBDOMAIN")
	token := os.Getenv("DUCKDNS_TOKEN")
	if sub == "" || token == "" {
		return errors.New("DUCKDNS_SUBDOMAIN and DUCKDNS_TOKEN are required unless RELAY_DEV=1")
	}
	host := duckdns.Hostname(sub)
	if err := duckdns.Update(sub, token, ""); err != nil {
		return err
	}
	go duckLoop(sub, token)

	certDir := os.Getenv("CERT_DIR")
	if certDir == "" {
		certDir = "/var/lib/channelflow-pin/certs"
	}
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return err
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(host),
		Cache:      autocert.DirCache(certDir),
		Email:      os.Getenv("ACME_EMAIL"),
	}
	tlsSrv := newHTTPServer(":443", h)
	tlsSrv.TLSConfig = &tls.Config{
		GetCertificate: m.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}
	httpSrv := newHTTPServer(":80", m.HTTPHandler(serveHTTPAPIOrRedirect(h)))

	errCh := make(chan error, 2)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	go func() { errCh <- tlsSrv.ListenAndServeTLS("", "") }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		_ = tlsSrv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:           addr,
		Handler:        h,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   11 * time.Minute,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 16 << 10,
		ErrorLog:       log.New(io.Discard, "", 0),
	}
}

func serveUntilSignal(httpSrv *http.Server) error {
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func duckLoop(sub, token string) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		_ = duckdns.Update(sub, token, "")
	}
}

func redirectHTTPS(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
}

// serveHTTPAPIOrRedirect keeps /health and /v1 on HTTP so POST and WebSocket
// are not turned into GET by a 301. The tester UI still moves to HTTPS.
func serveHTTPAPIOrRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		redirectHTTPS(w, r)
	})
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok")
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"waiting": s.hub.Waiting()})
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.web.ServeHTTP(w, r)
}

func (s *server) handleWait(w http.ResponseWriter, r *http.Request) {
	if websocket.IsWebSocketUpgrade(r) {
		s.handleWaitWS(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		sess, err := s.hub.Issue()
		if err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"pin":       sess.Pin,
			"display":   quickpin.DisplayPin(sess.Pin),
			"expiresIn": int(sess.ExpiresIn().Seconds()),
			"expiresAt": sess.ExpiresAt.Unix(),
		})
	case http.MethodGet:
		s.handleWaitPoll(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleWaitPoll(w http.ResponseWriter, r *http.Request) {
	pin := r.URL.Query().Get("pin")
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	ct, err := s.hub.Poll(ctx, pin)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]string{"type": "payload", "ciphertext": ct})
		return
	}
	if errors.Is(err, pins.ErrExpired) {
		writeJSON(w, http.StatusOK, map[string]string{"type": "expired"})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusOK, map[string]string{"type": "retry"})
		return
	}
	if errors.Is(err, context.Canceled) {
		s.hub.Cancel(pin)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (s *server) handleWaitWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sess, err := s.hub.Issue()
	if err != nil {
		_ = conn.WriteJSON(map[string]string{"type": "error"})
		return
	}

	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}

	if err := write(map[string]any{
		"type":      "pin",
		"pin":       sess.Pin,
		"display":   quickpin.DisplayPin(sess.Pin),
		"expiresIn": int(sess.ExpiresIn().Seconds()),
		"expiresAt": sess.ExpiresAt.Unix(),
	}); err != nil {
		s.hub.Cancel(sess.Pin)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		conn.SetReadLimit(256)
		_ = conn.SetReadDeadline(time.Now().Add(pins.Lifetime + time.Minute))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(pins.Lifetime + time.Minute))
			return nil
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				writeMu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				writeMu.Unlock()
			}
		}
	}()

	ct, err := s.hub.Wait(ctx, sess)
	if err != nil {
		if errors.Is(err, pins.ErrExpired) {
			_ = write(map[string]string{"type": "expired"})
		}
		return
	}
	_ = write(map[string]string{"type": "payload", "ciphertext": ct})
}

func (s *server) handlePins(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/pins/")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "deliver" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.hub.RateLimited() {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	var body deliverBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil || strings.TrimSpace(body.Ciphertext) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.hub.Deliver(parts[0], body.Ciphertext); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func listenAddr() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "43123"
	}
	host := os.Getenv("BIND")
	if host == "" {
		host = "0.0.0.0"
	}
	return net.JoinHostPort(host, port)
}

func envTruthy(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	return v == "1" || v == "true" || v == "yes"
}
