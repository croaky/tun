package tun

import "time"

const (
	PongWait   = 60 * time.Second
	PingPeriod = 20 * time.Second
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
