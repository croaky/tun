// Command tun is the tunnel client.
// Run this locally to forward requests from the tunnel server to a local service.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/croaky/tun"
)

type rule struct {
	method, path string
}

func main() {
	log.SetFlags(0)
	tun.LoadEnv(".env")

	server := strings.TrimSpace(os.Getenv("TUN_SERVER"))
	local := strings.TrimSpace(os.Getenv("TUN_LOCAL"))
	allow := strings.TrimSpace(os.Getenv("TUN_ALLOW"))
	token := strings.TrimSpace(os.Getenv("TUN_TOKEN"))

	if server == "" || local == "" || allow == "" || token == "" {
		log.Fatal("set TUN_SERVER, TUN_LOCAL, TUN_ALLOW, and TUN_TOKEN in environment or .env")
	}
	if _, err := url.ParseRequestURI(server); err != nil {
		log.Fatalf("invalid TUN_SERVER: %v", err)
	}

	rules, err := parseRules(strings.Fields(allow))
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	run(server, local, token, rules)
}

const (
	minDelay       = 500 * time.Millisecond
	maxDelay       = 30 * time.Second
	backoffReset   = 10 * time.Second
	requestTimeout = 30 * time.Second
	writeWait      = 5 * time.Second
)

// run connects to the tunnel server and forwards requests to the local service.
// It reconnects with exponential backoff on connection errors.
func run(server, local, token string, rules []rule) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	backoff := minDelay
	for {
		start := time.Now()
		if err := connect(server, local, token, rules, interrupt); err != nil {
			log.Printf("connection error: %v", err)
		}

		// Backoff with jitter; reset if the prior session lasted long enough
		if time.Since(start) > backoffReset {
			backoff = minDelay
		} else {
			if backoff < maxDelay {
				backoff = time.Duration(float64(backoff) * 1.6)
				if backoff > maxDelay {
					backoff = maxDelay
				}
			}
		}
		jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
		wait := backoff/2 + jitter
		log.Printf("reconnecting in %s...", wait)
		select {
		case <-interrupt:
			log.Println("interrupted")
			return
		case <-time.After(wait):
		}
	}
}

func connect(server, local, token string, rules []rule, interrupt chan os.Signal) error {
	log.Printf("connecting to %s", server)

	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.Dial(server, h)
	if err != nil {
		return fmt.Errorf("dial %s: %w", server, err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(tun.PongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(tun.PongWait))
		return nil
	})
	mu := &sync.Mutex{}

	log.Printf("connected, forwarding to %s", local)

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(tun.PingPeriod)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				mu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
				mu.Unlock()
				if err != nil {
					log.Printf("ping error: %v", err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	go func() {
		defer close(done)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("read error: %v", err)
				return
			}

			var req tun.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				log.Printf("invalid request: %v", err)
				continue
			}

			go handleRequest(conn, mu, local, rules, req)
		}
	}()

	select {
	case <-done:
		return fmt.Errorf("connection closed")
	case <-interrupt:
		mu.Lock()
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		mu.Unlock()
		return nil
	}
}

func handleRequest(conn *websocket.Conn, mu *sync.Mutex, local string, rules []rule, req tun.Request) {
	log.Printf("%s %s", req.Method, req.Path)

	resp := tun.Response{ID: req.ID}

	if !allowed(rules, req.Method, req.Path) {
		log.Printf("blocked: %s %s", req.Method, req.Path)
		resp.Status = http.StatusForbidden
		resp.Body = []byte("forbidden by tunnel filter")
		send(conn, mu, resp)
		return
	}

	r, err := http.NewRequest(req.Method, local+req.Path, bytes.NewReader(req.Body))
	if err != nil {
		resp.Status = http.StatusInternalServerError
		resp.Body = []byte(err.Error())
		send(conn, mu, resp)
		return
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}

	res, err := (&http.Client{Timeout: requestTimeout}).Do(r)
	if err != nil {
		log.Printf("local request error: %v", err)
		resp.Status = http.StatusBadGateway
		resp.Body = []byte(err.Error())
		send(conn, mu, resp)
		return
	}
	defer res.Body.Close()

	resp.Status = res.StatusCode
	resp.Headers = map[string][]string(res.Header)
	resp.Body, _ = io.ReadAll(res.Body)
	send(conn, mu, resp)
}

func send(conn *websocket.Conn, mu *sync.Mutex, resp tun.Response) {
	msg, err := json.Marshal(resp)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return
	}
	mu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, msg)
	mu.Unlock()
	if err != nil {
		log.Printf("write error: %v", err)
	}
}

func parseRules(args []string) ([]rule, error) {
	if len(args) == 0 || len(args)%2 != 0 {
		return nil, fmt.Errorf("TUN_ALLOW requires METHOD /path pairs")
	}
	var rules []rule
	for i := 0; i < len(args); i += 2 {
		rules = append(rules, rule{
			method: strings.ToUpper(args[i]),
			path:   args[i+1],
		})
	}
	return rules, nil
}

func allowed(rules []rule, method, path string) bool {
	for _, r := range rules {
		if r.method == method && r.path == path {
			return true
		}
	}
	return false
}
