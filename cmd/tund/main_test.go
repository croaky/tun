package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/croaky/tun"

	"github.com/croaky/is"
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
