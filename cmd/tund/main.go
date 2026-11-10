// Command tund is the tunnel server.
// Deploy this on a server to accept tunnel connections and proxy HTTP traffic.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

const responseTimeout = 30 * time.Second

// writeWait is how long a write to the tunnel can take. Tests change it.
var writeWait = 5 * time.Second

// tunnel is one tunnel connection. Its mutex puts writes in sequence.
// It is not server.mu, so a slow write does not stop other requests.
type tunnel struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// write sends msg to the tunnel peer. It fails when the peer does not
// take msg within writeWait.
func (t *tunnel) write(msg []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = t.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return t.conn.WriteMessage(websocket.TextMessage, msg)
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

func logf(user, format string, args ...any) {
	if user != "" {
		log.Printf("["+user+"] "+format, args...)
	} else {
		log.Printf(format, args...)
	}
}

type server struct {
	token   string
	mu      sync.RWMutex
	conn    *tunnel
	user    string
	pending map[string]chan tun.Response
}

func main() {
	log.SetFlags(0)
	tun.Load(".env")

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

	// The timeouts stop a slow client from holding a request open.
	// gorilla/websocket clears them on /tunnel when it upgrades.
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       responseTimeout,
	}
	log.Printf("tund listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
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
	defer conn.Close()
	conn.SetReadLimit(tun.MaxMessage)

	user := r.Header.Get("X-Tunnel-User")

	// Close existing connection if any (single tunnel at a time)
	tn := &tunnel{conn: conn}
	s.mu.Lock()
	oldConn := s.conn
	if oldConn != nil {
		logf(s.user, "new tunnel connection, closing previous")
	}
	s.conn = tn
	s.user = user
	s.mu.Unlock()

	// Close old connection outside of lock
	if oldConn != nil {
		_ = oldConn.conn.Close()
	}

	logf(user, "tunnel connected")

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
				// WriteControl is safe with other writes, so it takes no lock.
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
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
			replaced := s.conn != tn
			s.mu.RUnlock()
			normalClose := websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway)
			if !replaced && !normalClose {
				logf(user, "tunnel read error: %v", err)
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
	replaced := s.conn != tn
	if s.conn == tn {
		s.conn = nil
		s.user = ""
	}
	s.mu.Unlock()

	// Only log disconnect if not replaced (replacement logs its own message)
	if !replaced {
		logf(user, "tunnel disconnected")
	}
}

func (s *server) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	s.mu.RLock()
	conn := s.conn
	s.mu.RUnlock()

	if conn == nil {
		log.Printf("%d %s %s %.2fms", http.StatusServiceUnavailable, r.Method, r.URL.RequestURI(), ms(time.Since(start)))
		http.Error(w, "no tunnel connected", http.StatusServiceUnavailable)
		return
	}

	// Read request body, up to tun.MaxBody
	r.Body = http.MaxBytesReader(w, r.Body, tun.MaxBody)
	body, err := io.ReadAll(r.Body)
	if tooBig := (*http.MaxBytesError)(nil); errors.As(err, &tooBig) {
		log.Printf("%d %s %s %.2fms", http.StatusRequestEntityTooLarge, r.Method, r.URL.RequestURI(), ms(time.Since(start)))
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		log.Printf("%d %s %s %.2fms", http.StatusBadRequest, r.Method, r.URL.RequestURI(), ms(time.Since(start)))
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Send request to tunnel client
	reqID := newID()
	req := tun.Request{
		ID:      reqID,
		Method:  r.Method,
		Path:    r.URL.RequestURI(),
		Headers: r.Header,
		Body:    body,
	}

	msg, err := json.Marshal(req)
	if err != nil {
		log.Printf("%d %s %s %.2fms", http.StatusInternalServerError, r.Method, r.URL.RequestURI(), ms(time.Since(start)))
		http.Error(w, "marshal error", http.StatusInternalServerError)
		return
	}
	if len(msg) > tun.MaxMessage {
		log.Printf("%d %s %s %.2fms", http.StatusRequestEntityTooLarge, r.Method, r.URL.RequestURI(), ms(time.Since(start)))
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}

	respChan := make(chan tun.Response, 1)
	s.mu.Lock()
	s.pending[reqID] = respChan
	s.mu.Unlock()

	if err := conn.write(msg); err != nil {
		s.mu.Lock()
		delete(s.pending, reqID)
		s.mu.Unlock()
		s.drop(conn)
		log.Printf("%d %s %s %.2fms", http.StatusBadGateway, r.Method, r.URL.RequestURI(), ms(time.Since(start)))
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
		log.Printf("%d %s %s %.2fms", resp.Status, r.Method, r.URL.RequestURI(), ms(time.Since(start)))
	case <-time.After(responseTimeout):
		s.mu.Lock()
		delete(s.pending, reqID)
		s.mu.Unlock()
		log.Printf("%d %s %s %.2fms", http.StatusGatewayTimeout, r.Method, r.URL.RequestURI(), ms(time.Since(start)))
		http.Error(w, "tunnel timeout", http.StatusGatewayTimeout)
	}
}

// drop closes t after a failed write. The close stops each other write
// to t. If t is still the active tunnel, drop clears it, so the next
// request gets 503 until the client connects again.
func (s *server) drop(t *tunnel) {
	_ = t.conn.Close()
	s.mu.Lock()
	if s.conn == t {
		s.conn = nil
		s.user = ""
	}
	s.mu.Unlock()
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
