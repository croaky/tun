package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
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
