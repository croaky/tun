// Command tun is the tunnel client.
// Run this locally to forward requests from the tunnel server to a local service.
package main

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"log"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"slices"
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
	conn    *websocket.Conn
	mu      sync.Mutex
	local   string
	rules   []rule
	headers map[string]bool // canonical names that forward copies
	user    string
	http    *http.Client
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
	tun.Load(".env")

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
	headers, err := parseHeaders(strings.Fields(os.Getenv("TUN_HEADERS")))
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	log.Printf("forwarding headers: %s", strings.Join(slices.Sorted(maps.Keys(headers)), " "))

	run(server, local, token, rules, headers)
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
func run(server, local, token string, rules []rule, headers map[string]bool) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	attempt := 0
	for {
		err := connect(server, local, token, rules, headers, interrupt)
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

func connect(server, local, token string, rules []rule, headers map[string]bool, interrupt chan os.Signal) error {
	user := getUser()

	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set(tun.RevisionHeader, tun.Revision())
	if user != "" {
		h.Set("X-Tunnel-User", user)
	}

	conn, _, err := websocket.DefaultDialer.Dial(server, h)
	if err != nil {
		return fmt.Errorf("dial %s: %w", server, err)
	}
	defer conn.Close()

	c := &client{
		conn:    conn,
		local:   local,
		rules:   rules,
		headers: headers,
		user:    user,
		http:    newHTTPClient(),
	}

	c.logf("tun %s connected to %s, forwarding to %s", tun.Revision(), server, local)

	conn.SetReadLimit(tun.MaxMessage)

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
			if err := json.Unmarshal(msg, &req, tun.WireOptions); err != nil {
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

// newHTTPClient returns the client for requests to the local service.
// It does not follow redirects. TUN_ALLOW checks only the first
// request, so the client sends each 3xx back to the caller.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// baseHeaders are the request headers that the client always forwards.
// They describe the body and the caller. TUN_HEADERS adds to them.
var baseHeaders = []string{
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"Content-Encoding",
	"Content-Type",
	"User-Agent",
}

// overrideHeaders are headers that some frameworks read to change the
// method or the path of a request. TUN_ALLOW checks only the method
// and the path in the request line, so TUN_HEADERS cannot name them.
// The names are in canonical form. The client cannot see a method
// override in the body, such as the _method form field.
var overrideHeaders = []string{
	"X-Http-Method-Override",
	"X-Http-Method",
	"X-Method-Override",
	"X-Original-Url",
	"X-Rewrite-Url",
}

// parseHeaders returns the canonical names of the request headers that
// the client forwards: baseHeaders and names. The client drops each
// other header, so only a header that the operator chose reaches the
// local service. A new override header in some framework then needs no
// change here.
func parseHeaders(names []string) (map[string]bool, error) {
	headers := make(map[string]bool)
	for _, name := range append(slices.Clone(baseHeaders), names...) {
		k := http.CanonicalHeaderKey(name)
		if slices.Contains(overrideHeaders, k) {
			return nil, fmt.Errorf("TUN_HEADERS cannot hold %s, because it changes the method or the path that TUN_ALLOW checks", k)
		}
		headers[k] = true
	}
	return headers, nil
}

func (c *client) handleRequest(req tun.Request) {
	c.send(c.forward(req))
}

// forward checks req against TUN_ALLOW, sends it to the local service,
// and returns the response for the tunnel.
func (c *client) forward(req tun.Request) tun.Response {
	log.Printf("%s %s", req.Method, req.Path)

	resp := tun.Response{ID: req.ID}

	if !allowed(c.rules, req.Method, req.Path) {
		log.Printf("blocked: %s %s", req.Method, req.Path)
		resp.Status = http.StatusForbidden
		resp.Body = []byte("forbidden by tunnel filter")
		return resp
	}

	r, err := http.NewRequest(req.Method, c.local+req.Path, bytes.NewReader(req.Body))
	if err != nil {
		resp.Status = http.StatusInternalServerError
		resp.Body = []byte(err.Error())
		return resp
	}
	for k, vs := range req.Headers {
		if !c.headers[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}

	res, err := c.http.Do(r)
	if err != nil {
		log.Printf("local request error: %v", err)
		resp.Status = http.StatusBadGateway
		resp.Body = []byte(err.Error())
		return resp
	}
	defer res.Body.Close()

	resp.Status = res.StatusCode
	resp.Headers = map[string][]string(res.Header)
	// TUN_LOCAL names a service that the user trusts, so the client
	// takes its Content-Length up to the limit.
	resp.Body, err = tun.ReadBody(io.LimitReader(res.Body, tun.MaxBody+1), res.ContentLength, tun.MaxBody+1)
	if err != nil {
		log.Printf("read body error: %v", err)
		return tun.Response{ID: req.ID, Status: http.StatusBadGateway, Body: []byte(err.Error())}
	}
	if len(resp.Body) > tun.MaxBody {
		log.Printf("response body too large: %s %s", req.Method, req.Path)
		return tun.Response{ID: req.ID, Status: http.StatusBadGateway, Body: []byte("response body too large")}
	}
	return resp
}

func (c *client) send(resp tun.Response) {
	msg, err := json.Marshal(resp, tun.WireOptions)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return
	}
	if len(msg) > tun.MaxMessage {
		// tund closes the tunnel on a message above its read limit.
		log.Printf("response too large: %d bytes", len(msg))
		msg, _ = json.Marshal(tun.Response{ID: resp.ID, Status: http.StatusBadGateway, Body: []byte("response too large")}, tun.WireOptions)
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
