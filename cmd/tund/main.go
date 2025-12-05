// Command tund is the tunnel server.
// Deploy this on a server to accept tunnel connections and proxy HTTP traffic.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/croaky/tun"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	responseTimeout = 30 * time.Second
	writeWait       = 5 * time.Second
)

type server struct {
	token   string
	mu      sync.RWMutex
	conn    *websocket.Conn
	user    string
	pending map[string]chan tun.Response
}

func main() {
	log.SetFlags(0)
	tun.LoadEnv(".env")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	token := strings.TrimSpace(os.Getenv("TUN_TOKEN"))
	if token == "" {
		log.Fatal("TUN_TOKEN is required")
	}

	s := &server{
		token:   token,
		pending: make(map[string]chan tun.Response),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/tunnel", s.handleTunnel)
	mux.HandleFunc("/", s.handleRequest)

	log.Printf("tund listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func (s *server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	got := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(got, prefix) || strings.TrimSpace(strings.TrimPrefix(got, prefix)) != s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}

	user := r.Header.Get("X-Tunnel-User")

	// Close existing connection if any (single tunnel at a time)
	s.mu.Lock()
	oldConn := s.conn
	if oldConn != nil {
		oldUser := s.user
		if oldUser != "" {
			log.Printf("[%s] new tunnel connection, closing previous", oldUser)
		} else {
			log.Println("new tunnel connection, closing previous")
		}
	}
	s.conn = conn
	s.user = user
	s.mu.Unlock()

	// Close old connection outside of lock
	if oldConn != nil {
		_ = oldConn.Close()
	}

	if user != "" {
		log.Printf("[%s] tunnel connected", user)
	} else {
		log.Println("tunnel connected")
	}

	// Keepalive: reset read deadlines on pong
	conn.SetReadDeadline(time.Now().Add(tun.PongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(tun.PongWait))
		return nil
	})

	done := make(chan struct{})
	// Ping writer
	go func() {
		t := time.NewTicker(tun.PingPeriod)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.mu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
				s.mu.Unlock()
				if err != nil {
					log.Printf("tunnel ping error: %v", err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Read responses from client
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			// Don't log error if this connection was replaced by a new one
			s.mu.RLock()
			replaced := s.conn != conn
			s.mu.RUnlock()
			if !replaced {
				if user != "" {
					log.Printf("[%s] tunnel read error: %v", user, err)
				} else {
					log.Printf("tunnel read error: %v", err)
				}
			}
			break
		}

		var resp tun.Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			log.Printf("invalid response: %v", err)
			continue
		}

		s.mu.Lock()
		if ch, ok := s.pending[resp.ID]; ok {
			ch <- resp
			delete(s.pending, resp.ID)
		}
		s.mu.Unlock()
	}

	close(done)

	s.mu.Lock()
	replaced := s.conn != conn
	if s.conn == conn {
		s.conn = nil
		s.user = ""
	}
	s.mu.Unlock()

	// Only log disconnect if not replaced (replacement logs its own message)
	if !replaced {
		if user != "" {
			log.Printf("[%s] tunnel disconnected", user)
		} else {
			log.Println("tunnel disconnected")
		}
	}
}

func (s *server) handleRequest(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	conn := s.conn
	s.mu.RUnlock()

	if conn == nil {
		http.Error(w, "no tunnel connected", http.StatusServiceUnavailable)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	headers := map[string][]string(r.Header)

	// Create request ID and response channel
	reqID := newID()
	respChan := make(chan tun.Response, 1)

	s.mu.Lock()
	s.pending[reqID] = respChan
	s.mu.Unlock()

	// Send request to tunnel client
	req := tun.Request{
		ID:      reqID,
		Method:  r.Method,
		Path:    r.URL.RequestURI(),
		Headers: headers,
		Body:    body,
	}

	msg, err := json.Marshal(req)
	if err != nil {
		s.mu.Lock()
		delete(s.pending, reqID)
		s.mu.Unlock()
		http.Error(w, "marshal error", http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, msg)
	s.mu.Unlock()
	if err != nil {
		s.mu.Lock()
		delete(s.pending, reqID)
		s.mu.Unlock()
		http.Error(w, "tunnel write error", http.StatusBadGateway)
		return
	}

	// Wait for response with timeout
	select {
	case resp := <-respChan:
		for k, vals := range resp.Headers {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write(resp.Body)
	case <-time.After(responseTimeout):
		s.mu.Lock()
		delete(s.pending, reqID)
		s.mu.Unlock()
		http.Error(w, "tunnel timeout", http.StatusGatewayTimeout)
	}
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
