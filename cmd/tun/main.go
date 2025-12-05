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
	"os/exec"
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

type client struct {
	conn  *websocket.Conn
	mu    *sync.Mutex
	local string
	rules []rule
	user  string
}

func (c *client) logf(format string, args ...any) {
	if c.user != "" {
		log.Printf("["+c.user+"] "+format, args...)
	} else {
		log.Printf(format, args...)
	}
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

var delays = []time.Duration{
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

const (
	requestTimeout = 30 * time.Second
	writeWait      = 5 * time.Second
)

// run connects to the tunnel server and forwards requests to the local service.
// It reconnects with backoff on connection errors.
func run(server, local, token string, rules []rule) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	attempt := 0
	for {
		err := connect(server, local, token, rules, interrupt)
		if err == nil {
			return // graceful shutdown
		}
		log.Printf("connection error: %v", err)

		delay := delays[min(attempt, len(delays)-1)]
		delay = time.Duration(float64(delay) * (0.75 + rand.Float64()*0.5)) // ±25%
		log.Printf("reconnecting in %s...", delay)

		select {
		case <-interrupt:
			log.Println("interrupted")
			return
		case <-time.After(delay):
		}
		attempt++
	}
}

func connect(server, local, token string, rules []rule, interrupt chan os.Signal) error {
	user := getUser()

	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	if user != "" {
		h.Set("X-Tunnel-User", user)
	}

	conn, _, err := websocket.DefaultDialer.Dial(server, h)
	if err != nil {
		return fmt.Errorf("dial %s: %w", server, err)
	}
	defer conn.Close()

	c := &client{
		conn:  conn,
		mu:    &sync.Mutex{},
		local: local,
		rules: rules,
		user:  user,
	}

	c.logf("connected to %s, forwarding to %s", server, local)

	conn.SetReadDeadline(time.Now().Add(tun.PongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(tun.PongWait))
		return nil
	})

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(tun.PingPeriod)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.mu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
				c.mu.Unlock()
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

			go c.handleRequest(req)
		}
	}()

	select {
	case <-done:
		return fmt.Errorf("connection closed")
	case <-interrupt:
		c.mu.Lock()
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.mu.Unlock()
		return nil
	}
}

func (c *client) handleRequest(req tun.Request) {
	log.Printf("%s %s", req.Method, req.Path)

	resp := tun.Response{ID: req.ID}

	if !allowed(c.rules, req.Method, req.Path) {
		log.Printf("blocked: %s %s", req.Method, req.Path)
		resp.Status = http.StatusForbidden
		resp.Body = []byte("forbidden by tunnel filter")
		c.send(resp)
		return
	}

	r, err := http.NewRequest(req.Method, c.local+req.Path, bytes.NewReader(req.Body))
	if err != nil {
		resp.Status = http.StatusInternalServerError
		resp.Body = []byte(err.Error())
		c.send(resp)
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
		c.send(resp)
		return
	}
	defer res.Body.Close()

	resp.Status = res.StatusCode
	resp.Headers = map[string][]string(res.Header)
	resp.Body, err = io.ReadAll(res.Body)
	if err != nil {
		log.Printf("read body error: %v", err)
		resp.Status = http.StatusBadGateway
		resp.Body = []byte(err.Error())
	}
	c.send(resp)
}

func (c *client) send(resp tun.Response) {
	msg, err := json.Marshal(resp)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return
	}
	c.mu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, msg)
	c.mu.Unlock()
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

// getUser returns the tunnel user identifier.
// It first tries git config github.user, then falls back to $USER.
func getUser() string {
	out, err := exec.Command("git", "config", "--get", "github.user").Output()
	if err == nil {
		if user := strings.TrimSpace(string(out)); user != "" {
			return user
		}
	}
	return os.Getenv("USER")
}
