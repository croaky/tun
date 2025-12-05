package tun

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// pickFreePort reserves a free TCP port by binding :0 and closing.
func pickFreePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen :0: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	p := fmt.Sprintf("%d", addr.Port)
	_ = ln.Close()
	return p
}

func TestEndToEnd_TunnelForwardsRequest(t *testing.T) {
	// Local HTTP service to receive tunneled requests
	got := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/slack/events" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		select {
		case got <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(srv.Close)

	port := pickFreePort(t)
	wsURL := fmt.Sprintf("ws://127.0.0.1:%s/tunnel", port)
	httpURL := fmt.Sprintf("http://127.0.0.1:%s/health", port)
	forwardURL := fmt.Sprintf("http://127.0.0.1:%s/slack/events", port)

	// Start tund (absolute path, run from a clean temp dir so it won't read repo .env)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	absTund := "./cmd/tund"
	tund := exec.CommandContext(ctx, "go", "run", absTund)
	tund.Dir = root
	tund.Env = []string{"PORT=" + port, "TUN_TOKEN=itest", "PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	stderr, _ := tund.StderrPipe()
	stdout, _ := tund.StdoutPipe()
	if err := tund.Start(); err != nil {
		t.Fatalf("start tund: %v", err)
	}
	t.Cleanup(func() { _ = tund.Process.Kill() })

	// Wait for /health ready
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(httpURL)
		if err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	absTun := "./cmd/tun"
	tunCmd := exec.CommandContext(ctx, "go", "run", absTun)
	tunCmd.Dir = root
	tunCmd.Env = []string{
		"TUN_SERVER=" + wsURL,
		"TUN_LOCAL=" + srv.URL,
		"TUN_ALLOW=POST /slack/events",
		"TUN_TOKEN=itest",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	tunStdout, _ := tunCmd.StdoutPipe()
	tunStderr, _ := tunCmd.StderrPipe()
	if err := tunCmd.Start(); err != nil {
		t.Fatalf("start tun: %v", err)
	}
	t.Cleanup(func() { _ = tunCmd.Process.Kill() })

	// Poll until tunnel connected (server stops returning 503), then assert 200/ok
	readyDeadline := time.Now().Add(8 * time.Second)
	for {
		resp, err := http.Post(forwardURL, "application/json", strings.NewReader("{}"))
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusServiceUnavailable { // not 503 => connected
				if resp.StatusCode != 200 || resp.Header.Get("X-Test") != "ok" {
					_ = dump("tund", stdout, stderr)
					_ = dump("tun", tunStdout, tunStderr)
					t.Fatalf("forward status=%d header[X-Test]=%q body=%s", resp.StatusCode, resp.Header.Get("X-Test"), string(b))
				}
				break
			}
		}
		if time.Now().After(readyDeadline) {
			_ = dump("tund", stdout, stderr)
			_ = dump("tun", tunStdout, tunStderr)
			t.Fatal("tunnel did not become ready in time")
		}
		time.Sleep(150 * time.Millisecond)
	}

	select {
	case <-got:
		// ok
	case <-time.After(2 * time.Second):
		_ = dump("tund", stdout, stderr)
		_ = dump("tun", tunStdout, tunStderr)
		t.Fatal("local server did not receive request")
	}
}

func dump(name string, out, err io.Reader) error {
	brOut := bufio.NewScanner(out)
	brErr := bufio.NewScanner(err)
	for brOut.Scan() {
		fmt.Printf("[%s stdout] %s\n", name, brOut.Text())
	}
	for brErr.Scan() {
		fmt.Printf("[%s stderr] %s\n", name, brErr.Text())
	}
	return nil
}
