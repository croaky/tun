package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/croaky/tun"
)

func TestHandleTunnelAuthUnauthorized(t *testing.T) {
	s := &server{
		token:   "secret",
		pending: make(map[string]chan tun.Response),
	}
	r := httptest.NewRequest(http.MethodGet, "/tunnel", nil)
	rw := httptest.NewRecorder()

	s.handleTunnel(rw, r)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", rw.Code, http.StatusUnauthorized)
	}
}

func TestNewID(t *testing.T) {
	id := newID()

	// Should be 32 hex chars (16 bytes)
	if len(id) != 32 {
		t.Errorf("newID() length = %d, want 32", len(id))
	}

	// Should be valid hex
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("newID() contains non-hex char: %c", c)
		}
	}

	// Should be unique
	id2 := newID()
	if id == id2 {
		t.Error("newID() returned duplicate IDs")
	}
}

func TestHandleRequest_NoTunnel(t *testing.T) {
	s := &server{
		token:   "secret",
		pending: make(map[string]chan tun.Response),
		// conn is nil - no tunnel connected
	}

	r := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(`{}`))
	rw := httptest.NewRecorder()

	s.handleRequest(rw, r)

	if rw.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want %d", rw.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rw.Body.String(), "no tunnel connected") {
		t.Errorf("body = %q, want 'no tunnel connected'", rw.Body.String())
	}
}
