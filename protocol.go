// Package tun provides shared types and utilities for the tun tunnel system.
// It defines the protocol messages exchanged between the tunnel client and server.
package tun

import "time"

// WebSocket keepalive constants.
// PingPeriod must be less than PongWait to ensure pings are sent before
// the read deadline expires. The 20s/60s ratio gives ample margin for
// network latency while detecting dead connections within a minute.
const (
	PongWait   = 60 * time.Second
	PingPeriod = 20 * time.Second
)

// Size limits for the tunnel.
// MaxBody is the largest request or response body that the tunnel
// carries. MaxMessage is the largest WebSocket message that each side
// reads. JSON encodes a body in base64, which adds one third, and the
// rest of MaxMessage is for the headers.
const (
	MaxBody    = 1 << 20
	MaxMessage = 2 * MaxBody
)

// Request is sent from server to client through the WebSocket tunnel.
type Request struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

// Response is sent from client to server through the WebSocket tunnel.
type Response struct {
	ID      string              `json:"id"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}
