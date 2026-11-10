package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/croaky/is"

	"github.com/croaky/tun"
)

func TestHandleTunnelAuthUnauthorized(t *testing.T) {
	is := is.New(t)

	s := &server{
		token:   "secret",
		pending: make(map[string]chan tun.Response),
	}
	r := httptest.NewRequest(http.MethodGet, "/tunnel", nil)
	rw := httptest.NewRecorder()

	s.handleTunnel(rw, r)

	is.Eq(rw.Code, http.StatusUnauthorized)
}

func TestNewID(t *testing.T) {
	is := is.New(t)

	id := newID()

	// Should be 32 hex chars (16 bytes)
	is.Eq(len(id), 32)

	// Should be valid hex
	for _, c := range id {
		is.True(strings.ContainsRune("0123456789abcdef", c))
	}

	// Should be unique
	id2 := newID()
	is.NotEq(id, id2)
}

func TestHandleRequest_NoTunnel(t *testing.T) {
	is := is.New(t)

	s := &server{
		token:   "secret",
		pending: make(map[string]chan tun.Response),
		// conn is nil - no tunnel connected
	}

	r := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(`{}`))
	rw := httptest.NewRecorder()

	s.handleRequest(rw, r)

	is.Eq(rw.Code, http.StatusServiceUnavailable)
	is.True(strings.Contains(rw.Body.String(), "no tunnel connected"))
}

// startTunnelServer serves s.handleTunnel with small TCP send buffers,
// so that a large write blocks when the peer does not read.
func startTunnelServer(t *testing.T, s *server) string {
	t.Helper()
	ts := httptest.NewUnstartedServer(http.HandlerFunc(s.handleTunnel))
	ts.Config.ConnState = func(c net.Conn, state http.ConnState) {
		if state == http.StateNew {
			_ = c.(*net.TCPConn).SetWriteBuffer(4096)
		}
	}
	ts.Start()
	t.Cleanup(ts.Close)
	return "ws" + strings.TrimPrefix(ts.URL, "http")
}

// dialPeer connects a tunnel peer with a small TCP read buffer. The
// peer does not read unless the test reads.
func dialPeer(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	d := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err == nil {
				_ = c.(*net.TCPConn).SetReadBuffer(4096)
			}
			return c, err
		},
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	conn, _, err := d.Dial(url, h)
	is.New(t).NoErr(err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func newServer() *server {
	return &server{token: "secret", pending: make(map[string]chan tun.Response)}
}

// waitConn waits until the active tunnel is not old.
func waitConn(t *testing.T, s *server, old *tunnel, within time.Duration) *tunnel {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		c := s.conn
		s.mu.RUnlock()
		if c != nil && c != old {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no new tunnel within %s", within)
	return nil
}

func post(s *server, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
	rw := httptest.NewRecorder()
	s.handleRequest(rw, r)
	return rw
}

func TestHandleRequestBodyTooLarge(t *testing.T) {
	is := is.New(t)
	s := newServer()
	peer := dialPeer(t, startTunnelServer(t, s))
	waitConn(t, s, nil, time.Second)

	rw := post(s, make([]byte, tun.MaxBody+1))

	is.Eq(rw.Code, http.StatusRequestEntityTooLarge)
	_ = peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := peer.ReadMessage()
	is.HasErr(err) // the peer got no message
}

// TestHandleRequestBodyTooLargeNoLength checks the 413 for a body
// with no Content-Length, which tund knows only as it reads.
func TestHandleRequestBodyTooLargeNoLength(t *testing.T) {
	is := is.New(t)
	s := newServer()
	peer := dialPeer(t, startTunnelServer(t, s))
	waitConn(t, s, nil, time.Second)

	r := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(make([]byte, tun.MaxBody+1)))
	r.ContentLength = -1
	rw := httptest.NewRecorder()
	s.handleRequest(rw, r)

	is.Eq(rw.Code, http.StatusRequestEntityTooLarge)
	_ = peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := peer.ReadMessage()
	is.HasErr(err) // the peer got no message
}

func TestHandleRequestStalledPeer(t *testing.T) {
	is := is.New(t)
	defer func(d time.Duration) { writeWait = d }(writeWait)
	writeWait = 2 * time.Second

	s := newServer()
	url := startTunnelServer(t, s)
	dialPeer(t, url) // never reads
	old := waitConn(t, s, nil, time.Second)

	big := make(chan int, 1)
	go func() { big <- post(s, make([]byte, tun.MaxBody)).Code }()

	// Wait until the big request holds the write lock. Its write then
	// blocks, because the peer does not read.
	for deadline := time.Now().Add(5 * time.Second); old.mu.TryLock(); time.Sleep(10 * time.Millisecond) {
		old.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("big request did not start its write")
		}
	}

	small := make(chan int, 1)
	go func() { small <- post(s, []byte("{}")).Code }()

	// Wait until both requests use the old tunnel.
	for deadline := time.Now().Add(time.Second); ; time.Sleep(10 * time.Millisecond) {
		s.mu.RLock()
		n := len(s.pending)
		s.mu.RUnlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d pending requests, want 2", n)
		}
	}

	// A new tunnel with the token replaces the stalled one at once.
	dialPeer(t, url)
	waitConn(t, s, old, 500*time.Millisecond)

	for name, ch := range map[string]chan int{"big": big, "small": small} {
		select {
		case code := <-ch:
			is.True(code == http.StatusBadGateway ||
				code == http.StatusServiceUnavailable ||
				code == http.StatusGatewayTimeout)
		case <-time.After(3 * time.Second):
			t.Fatalf("%s request did not return", name)
		}
	}
}

func TestHandleRequestWriteDeadline(t *testing.T) {
	is := is.New(t)
	defer func(d time.Duration) { writeWait = d }(writeWait)
	writeWait = 200 * time.Millisecond

	s := newServer()
	dialPeer(t, startTunnelServer(t, s)) // never reads
	waitConn(t, s, nil, time.Second)

	done := make(chan int, 1)
	go func() { done <- post(s, make([]byte, tun.MaxBody)).Code }()

	select {
	case code := <-done:
		is.Eq(code, http.StatusBadGateway)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return within the write deadline")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	is.True(s.conn == nil) // the server dropped the stalled tunnel
}

// echoPeer connects a tunnel peer that answers each request with 200
// and a short body, as the tun client does for a Slack event.
func echoPeer(b *testing.B, url string) {
	b.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	conn, _, err := websocket.DefaultDialer.Dial(url, h)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req tun.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				return
			}
			out, _ := json.Marshal(tun.Response{
				ID:      req.ID,
				Status:  http.StatusOK,
				Headers: map[string][]string{"Content-Type": {"text/plain"}},
				Body:    []byte("ok"),
			})
			if err := conn.WriteMessage(websocket.TextMessage, out); err != nil {
				return
			}
		}
	}()
}

func quietLog(b *testing.B) {
	b.Helper()
	log.SetOutput(io.Discard)
	b.Cleanup(func() { log.SetOutput(os.Stderr) })
}

// BenchmarkHandleRequest sends a POST through tund and a tunnel peer.
// The sizes are a Slack event, a large form, and tun.MaxBody.
func BenchmarkHandleRequest(b *testing.B) {
	quietLog(b)
	for _, size := range []int{1 << 10, 64 << 10, tun.MaxBody} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			s := newServer()
			ts := httptest.NewServer(http.HandlerFunc(s.handleTunnel))
			b.Cleanup(ts.Close)
			echoPeer(b, "ws"+strings.TrimPrefix(ts.URL, "http"))
			for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
				s.mu.RLock()
				c := s.conn
				s.mu.RUnlock()
				if c != nil {
					break
				}
				if time.Now().After(deadline) {
					b.Fatal("no tunnel")
				}
			}

			body := bytes.Repeat([]byte(`{"type":"event_callback"}`), size/25+1)[:size]
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				r := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-Slack-Signature", "v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503")
				r.Header.Set("X-Slack-Request-Timestamp", "1531420618")
				rw := httptest.NewRecorder()
				s.handleRequest(rw, r)
				if rw.Code != http.StatusOK {
					b.Fatalf("status %d", rw.Code)
				}
			}
		})
	}
}

// BenchmarkHandleRequestTooLarge measures the 413 path for a body
// above tun.MaxBody.
func BenchmarkHandleRequestTooLarge(b *testing.B) {
	quietLog(b)
	s := newServer()
	s.conn = &tunnel{} // the 413 returns before a write
	body := make([]byte, tun.MaxBody+1)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		if rw := post(s, body); rw.Code != http.StatusRequestEntityTooLarge {
			b.Fatalf("status %d", rw.Code)
		}
	}
}

func TestHandleTunnelReadLimit(t *testing.T) {
	s := newServer()
	peer := dialPeer(t, startTunnelServer(t, s))
	waitConn(t, s, nil, time.Second)

	_ = peer.WriteMessage(websocket.TextMessage, make([]byte, tun.MaxMessage+1))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		c := s.conn
		s.mu.RUnlock()
		if c == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the server kept the tunnel open")
}
