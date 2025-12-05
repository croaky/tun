package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/croaky/tun"
)

func TestHandleTunnelAuthUnauthorized(t *testing.T) {
	os.Setenv("TUN_TOKEN", "secret")
	t.Cleanup(func() { os.Unsetenv("TUN_TOKEN") })

	s := &server{pending: make(map[string]chan tun.Response)}
	r := httptest.NewRequest(http.MethodGet, "/tunnel", nil)
	rw := httptest.NewRecorder()

	s.handleTunnel(rw, r)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", rw.Code, http.StatusUnauthorized)
	}
}
