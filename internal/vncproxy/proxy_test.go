package vncproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestStartDisabledNoop(t *testing.T) {
	proxy := New(Options{StateDir: t.TempDir()})

	result, err := proxy.Start(context.Background(), Request{Enabled: false})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.Started {
		t.Fatal("Started = true")
	}
}

func TestStartValidatesStaticAssets(t *testing.T) {
	proxy := New(Options{StateDir: t.TempDir(), AssetRoots: []string{filepath.Join(t.TempDir(), "missing")}})

	_, err := proxy.Start(context.Background(), Request{Enabled: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "noVNC static assets not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartServesHTTPSWithSelectedAssets(t *testing.T) {
	root := writeAssets(t)
	proxy := New(Options{
		StateDir:      t.TempDir(),
		AssetRoots:    []string{root},
		ListenAddress: "127.0.0.1:0",
	})

	result, err := proxy.Start(context.Background(), Request{Enabled: true, VNCPort: 5901})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer stopProxy(t, proxy)
	if !result.Started {
		t.Fatal("Started = false")
	}
	if result.AssetRoot != root {
		t.Fatalf("AssetRoot = %q", result.AssetRoot)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := client.Get("https://" + result.ListenAddress + "/vnc.html")
	if err != nil {
		t.Fatalf("GET vnc.html: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "novnc" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, string(body))
	}
}

func TestHandlerServesStaticAssets(t *testing.T) {
	root := writeAssets(t)
	proxy := New(Options{StateDir: t.TempDir()})
	server := httptest.NewServer(proxy.Handler(HandlerOptions{AssetRoot: root, TargetAddress: "127.0.0.1:5900"}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/vnc.html")
	if err != nil {
		t.Fatalf("GET vnc.html: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "novnc" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, string(body))
	}
}

func TestWebSocketTargetFailureReturnsBadGateway(t *testing.T) {
	root := writeAssets(t)
	proxy := New(Options{
		StateDir: t.TempDir(),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial failed")
		},
	})
	server := httptest.NewServer(proxy.Handler(HandlerOptions{AssetRoot: root, TargetAddress: "127.0.0.1:5900"}))
	defer server.Close()

	_, resp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/websockify", nil)
	if err == nil {
		t.Fatal("expected dial error")
	}
	if resp == nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %#v err = %v", resp, err)
	}
}

func TestWebSocketBridgeCopiesBothDirections(t *testing.T) {
	root := writeAssets(t)
	vncListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen VNC: %v", err)
	}
	defer vncListener.Close()
	vncDone := make(chan error, 1)
	go func() {
		conn, err := vncListener.Accept()
		if err != nil {
			vncDone <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			vncDone <- err
			return
		}
		if line != "client\n" {
			vncDone <- errors.New("unexpected VNC input: " + line)
			return
		}
		_, err = conn.Write([]byte("server\n"))
		vncDone <- err
	}()
	proxy := New(Options{StateDir: t.TempDir()})
	server := httptest.NewServer(proxy.Handler(HandlerOptions{AssetRoot: root, TargetAddress: vncListener.Addr().String()}))
	defer server.Close()

	wsConn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/websockify", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	netConn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
	defer netConn.Close()
	if _, err := netConn.Write([]byte("client\n")); err != nil {
		t.Fatalf("write websocket netconn: %v", err)
	}
	line, err := bufio.NewReader(netConn).ReadString('\n')
	if err != nil {
		t.Fatalf("read websocket netconn: %v", err)
	}
	if line != "server\n" {
		t.Fatalf("line = %q", line)
	}
	if err := <-vncDone; err != nil {
		t.Fatalf("VNC side error: %v", err)
	}
}

func writeAssets(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "vnc.html"), []byte("novnc"), 0o644); err != nil {
		t.Fatalf("write vnc.html: %v", err)
	}
	return root
}

func stopProxy(t *testing.T, proxy *Proxy) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := proxy.Stop(ctx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}
