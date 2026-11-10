// Package tun provides shared types and utilities for the tun tunnel system.
// It defines the protocol messages exchanged between the tunnel client and server.
package tun

import (
	"io"
	"time"
)

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

// ReadBody reads r to io.EOF and returns the bytes. It does not limit
// the length. Put a limit on r, such as http.MaxBytesReader.
//
// size is the body length that the sender gave, or -1 when the sender
// gave none. io.ReadAll starts at 512 bytes and grows many times for a
// large body. When size is known and at most prealloc, ReadBody
// allocates the buffer once. Otherwise it uses io.ReadAll, so a false
// size does not make it hold memory for data that the sender did not
// send.
func ReadBody(r io.Reader, size int64, prealloc int) ([]byte, error) {
	if size < 0 || size > int64(prealloc) {
		return io.ReadAll(r)
	}
	// The +1 lets the read that returns io.EOF use the buffer as is.
	b := make([]byte, 0, size+1)
	for {
		m, err := r.Read(b[len(b):cap(b)])
		b = b[:len(b)+m]
		if err == io.EOF {
			return b, nil
		}
		if err != nil {
			return b, err
		}
		if len(b) == cap(b) {
			// The body is longer than size.
			rest, err := io.ReadAll(r)
			return append(b, rest...), err
		}
	}
}
