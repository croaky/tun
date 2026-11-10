package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/croaky/is"

	"github.com/croaky/tun"
)

func TestForwardResponseTooLarge(t *testing.T) {
	is := is.New(t)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, tun.MaxBody+1))
	}))
	t.Cleanup(local.Close)
	rules, err := parseRules([]string{"GET", "/big"})
	is.NoErr(err)
	c := &client{local: local.URL, rules: rules, http: newHTTPClient()}

	resp := c.forward(tun.Request{ID: "1", Method: "GET", Path: "/big"})

	is.Eq(resp.Status, http.StatusBadGateway)
	is.True(len(resp.Body) < tun.MaxBody)
}

// TestConnectReadLimit checks that the client closes the tunnel when the
// server sends a message above tun.MaxMessage.
func TestConnectReadLimit(t *testing.T) {
	var upgrader websocket.Upgrader
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, make([]byte, tun.MaxMessage+1))
		time.Sleep(3 * time.Second) // keep the connection open
	}))
	t.Cleanup(srv.Close)

	done := make(chan error, 1)
	go func() {
		url := "ws" + strings.TrimPrefix(srv.URL, "http")
		done <- connect(url, "http://127.0.0.1:1", "secret", nil, make(chan os.Signal))
	}()

	select {
	case err := <-done:
		is.New(t).HasErr(err)
	case <-time.After(2 * time.Second):
		t.Fatal("client kept the tunnel open")
	}
}

func TestParseRules(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		wantLen int
	}{
		{"valid single", []string{"POST", "/slack/events"}, false, 1},
		{"valid multiple", []string{"POST", "/slack/events", "GET", "/health"}, false, 2},
		{"lowercase normalized", []string{"post", "/slack/events"}, false, 1},
		{"empty", []string{}, true, 0},
		{"nil", nil, true, 0},
		{"odd count", []string{"POST"}, true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := is.New(t)

			rules, err := parseRules(tt.args)
			if tt.wantErr {
				is.HasErr(err)
				return
			}
			is.NoErr(err)
			is.Eq(len(rules), tt.wantLen)
		})
	}
}

func TestAllowed(t *testing.T) {
	rules, err := parseRules([]string{"POST", "/slack/events", "GET", "/health"})
	is.New(t).NoErr(err)

	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{"POST", "/slack/events", true},
		{"GET", "/health", true},
		{"GET", "/slack/events", false},
		{"POST", "/health", false},
		{"POST", "/other", false},
		{"DELETE", "/slack/events", false},
		{"POST", "/slack/events/", false},
		{"POST", "/Slack/Events", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			is := is.New(t)

			is.Eq(allowed(rules, tt.method, tt.path), tt.want)
		})
	}
}

func quietLog(b *testing.B) {
	b.Helper()
	log.SetOutput(io.Discard)
	b.Cleanup(func() { log.SetOutput(os.Stderr) })
}

var benchSizes = []int{1 << 10, 64 << 10, tun.MaxBody}

// BenchmarkForward sends a Slack event to a local service through
// forward. The sizes are for the response body, up to tun.MaxBody.
func BenchmarkForward(b *testing.B) {
	quietLog(b)
	for _, size := range benchSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			out := bytes.Repeat([]byte("x"), size)
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write(out)
			}))
			b.Cleanup(local.Close)
			rules, err := parseRules([]string{"POST", "/slack/events", "GET", "/health"})
			if err != nil {
				b.Fatal(err)
			}
			c := &client{local: local.URL, rules: rules, http: newHTTPClient()}
			req := tun.Request{
				ID:     "0123456789abcdef0123456789abcdef",
				Method: "POST",
				Path:   "/slack/events",
				Headers: map[string][]string{
					"Content-Type":              {"application/json"},
					"X-Slack-Request-Timestamp": {"1531420618"},
				},
				Body: []byte(`{"type":"event_callback","event":{"type":"message"}}`),
			}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				if resp := c.forward(req); resp.Status != http.StatusOK {
					b.Fatalf("status %d", resp.Status)
				}
			}
		})
	}
}

// BenchmarkForwardBlocked measures a request that TUN_ALLOW blocks.
func BenchmarkForwardBlocked(b *testing.B) {
	quietLog(b)
	rules, err := parseRules([]string{"POST", "/slack/events", "GET", "/health"})
	if err != nil {
		b.Fatal(err)
	}
	c := &client{local: "http://127.0.0.1:1", rules: rules}
	req := tun.Request{ID: "1", Method: "GET", Path: "/admin"}
	b.ReportAllocs()
	for b.Loop() {
		if resp := c.forward(req); resp.Status != http.StatusForbidden {
			b.Fatalf("status %d", resp.Status)
		}
	}
}

// BenchmarkSend writes a response to a tunnel peer that discards it.
func BenchmarkSend(b *testing.B) {
	quietLog(b)
	var upgrader websocket.Upgrader
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadLimit(tun.MaxMessage)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}))
	b.Cleanup(srv.Close)
	for _, size := range benchSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = conn.Close() })
			c := &client{conn: conn}
			resp := tun.Response{
				ID:      "0123456789abcdef0123456789abcdef",
				Status:  http.StatusOK,
				Headers: map[string][]string{"Content-Type": {"text/plain"}},
				Body:    bytes.Repeat([]byte("x"), size),
			}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				c.send(resp)
			}
		})
	}
}

// TestForwardDoesNotFollowRedirects checks that the client sends a 3xx
// from the local service back to the caller. The client must not send a
// request to the redirect target, because TUN_ALLOW did not check it.
func TestForwardDoesNotFollowRedirects(t *testing.T) {
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		_, _ = w.Write([]byte("secret"))
	}))
	t.Cleanup(other.Close)
	otherURL, err := url.Parse(other.URL)
	is.New(t).NoErr(err)

	var location string
	var status int
	var privateHits atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/private" {
			privateHits.Add(1)
			_, _ = w.Write([]byte("secret"))
			return
		}
		w.Header().Set("Location", location)
		w.WriteHeader(status)
	}))
	t.Cleanup(local.Close)

	rules, err := parseRules([]string{"POST", "/slack/events"})
	is.New(t).NoErr(err)
	c := &client{local: local.URL, rules: rules, http: newHTTPClient()}

	targets := map[string]string{
		"same service": "/private",
		"other port":   other.URL + "/",
		"other host":   "http://localhost:" + otherURL.Port() + "/",
	}
	for _, code := range []int{301, 302, 303, 307, 308} {
		for name, target := range targets {
			t.Run(fmt.Sprintf("%d %s", code, name), func(t *testing.T) {
				is := is.New(t)
				status, location = code, target
				privateHits.Store(0)
				otherHits.Store(0)

				resp := c.forward(tun.Request{
					ID:     "1",
					Method: "POST",
					Path:   "/slack/events",
					Body:   []byte("{}"),
				})

				is.Eq(resp.Status, code)
				is.Eq(http.Header(resp.Headers).Get("Location"), target)
				is.Eq(privateHits.Load(), int32(0))
				is.Eq(otherHits.Load(), int32(0))
			})
		}
	}
}
