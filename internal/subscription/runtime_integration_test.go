package subscription

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
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// Run with MIHOMO_TEST_BINARY pointing at an independently verified official
// binary. The entire forwarding test stays on loopback, using two fake exits.
func TestMihomoMultipleListenersUseTheirAssignedExit(t *testing.T) {
	binary := os.Getenv("MIHOMO_TEST_BINARY")
	if binary == "" {
		t.Skip("MIHOMO_TEST_BINARY not provided")
	}
	var nodes []Node
	for _, label := range []string{"exit-one", "exit-two"} {
		label := label
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				http.Error(w, "CONNECT required", 400)
				return
			}
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			if rw.Flush() != nil {
				return
			}
			if _, err := http.ReadRequest(rw.Reader); err != nil {
				return
			}
			_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(label), label)
			_ = rw.Flush()
		}))
		t.Cleanup(server.Close)
		host, port, _ := net.SplitHostPort(server.Listener.Addr().String())
		p, _ := strconv.Atoi(port)
		nodes = append(nodes, Node{Name: label, Type: "http", Proxy: map[string]any{"type": "http", "server": host, "port": p}})
	}
	var ports []int
	for i := 0; i < 2; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
		_ = listener.Close()
	}
	data, err := Render(nodes, []string{"one", "two"}, ports)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, binary, "-t", "-d", dir, "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("mihomo validation failed: %v %s", err, out)
	}
	cmd := exec.CommandContext(ctx, binary, "-d", dir, "-f", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for i, port := range ports {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		var conn net.Conn
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			dialer, err := proxy.SOCKS5("tcp", addr, nil, &net.Dialer{Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			conn, err = dialer.Dial("tcp", "test.invalid:80")
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if conn == nil {
			t.Fatalf("listener %d did not start or could not reach its exit", i)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test.invalid\r\nConnection: close\r\n\r\n")
		if err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		_ = conn.Close()
		if err != nil || string(body) != []string{"exit-one", "exit-two"}[i] {
			t.Fatalf("listener %d used wrong exit: %q (%v)", i, body, err)
		}
	}
}
