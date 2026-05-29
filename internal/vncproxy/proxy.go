package vncproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/coder/websocket"
	"github.com/munenick/docker-vm-runner/internal/certs"
)

const (
	DefaultAssetRoot = "/usr/share/docker-vm-runner/novnc"
	LegacyAssetRoot  = "/usr/share/novnc"
)

type Options struct {
	StateDir      string
	CertDir       string
	AssetRoots    []string
	ListenAddress string
	DialContext   func(context.Context, string, string) (net.Conn, error)
}

type Request struct {
	Enabled       bool
	NoVNCPort     int
	VNCPort       int
	AssetRoot     string
	TargetAddress string
}

type Result struct {
	Started       bool
	AssetRoot     string
	ListenAddress string
	TargetAddress string
	CertPath      string
	KeyPath       string
}

type Proxy struct {
	Options      Options
	server       *http.Server
	listener     net.Listener
	serverCancel context.CancelFunc
}

func New(opts Options) *Proxy {
	applyDefaults(&opts)
	return &Proxy{Options: opts}
}

func applyDefaults(opts *Options) {
	if opts.StateDir == "" {
		opts.StateDir = "/var/lib/docker-vm-runner"
	}
	if opts.CertDir == "" {
		opts.CertDir = filepath.Join(opts.StateDir, "certs")
	}
	if len(opts.AssetRoots) == 0 {
		opts.AssetRoots = []string{DefaultAssetRoot, LegacyAssetRoot}
	}
	if opts.DialContext == nil {
		dialer := net.Dialer{}
		opts.DialContext = dialer.DialContext
	}
}

func (p *Proxy) Start(ctx context.Context, req Request) (Result, error) {
	if !req.Enabled {
		return Result{}, nil
	}
	assetRoot, err := p.resolveAssetRoot(req.AssetRoot)
	if err != nil {
		return Result{}, err
	}
	target := targetAddress(req)
	listenAddress := p.Options.ListenAddress
	if listenAddress == "" {
		listenAddress = net.JoinHostPort("0.0.0.0", strconv.Itoa(defaultPort(req.NoVNCPort, 6080)))
	}
	certPath := filepath.Join(p.Options.CertDir, "sushy.crt")
	keyPath := filepath.Join(p.Options.CertDir, "sushy.key")
	if err := certs.EnsureSelfSigned(certs.Request{CertPath: certPath, KeyPath: keyPath}); err != nil {
		return Result{}, err
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return Result{}, fmt.Errorf("load noVNC certificate: %w", err)
	}
	listener, err := tls.Listen("tcp", listenAddress, &tls.Config{Certificates: []tls.Certificate{certificate}})
	if err != nil {
		return Result{}, fmt.Errorf("listen for noVNC: %w", err)
	}
	serverCtx, serverCancel := context.WithCancel(ctx)
	p.listener = listener
	p.serverCancel = serverCancel
	p.server = &http.Server{
		Handler:     p.Handler(HandlerOptions{AssetRoot: assetRoot, TargetAddress: target}),
		BaseContext: func(net.Listener) context.Context { return serverCtx },
	}
	go func() {
		err := p.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(os.Stderr, "noVNC server failed: %v\n", err)
		}
	}()
	return Result{
		Started:       true,
		AssetRoot:     assetRoot,
		ListenAddress: listener.Addr().String(),
		TargetAddress: target,
		CertPath:      certPath,
		KeyPath:       keyPath,
	}, nil
}

func (p *Proxy) Stop(ctx context.Context) error {
	if p.server == nil {
		return nil
	}
	if p.serverCancel != nil {
		p.serverCancel()
	}
	err := p.server.Shutdown(ctx)
	p.server = nil
	p.listener = nil
	p.serverCancel = nil
	return err
}

type HandlerOptions struct {
	AssetRoot     string
	TargetAddress string
}

func (p *Proxy) Handler(opts HandlerOptions) http.Handler {
	fileServer := http.FileServer(http.Dir(opts.AssetRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/websockify" {
			p.serveWebSocket(w, r, opts.TargetAddress)
			return
		}
		if r.URL.Path == "/" && fileExists(filepath.Join(opts.AssetRoot, "vnc.html")) {
			http.Redirect(w, r, "/vnc.html", http.StatusFound)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (p *Proxy) serveWebSocket(w http.ResponseWriter, r *http.Request, target string) {
	targetConn, err := p.Options.DialContext(r.Context(), "tcp", target)
	if err != nil {
		http.Error(w, fmt.Sprintf("connect to VNC target: %v", err), http.StatusBadGateway)
		return
	}
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		_ = targetConn.Close()
		return
	}
	defer wsConn.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	websocketConn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	_ = bridge(websocketConn, targetConn)
}

func (p *Proxy) resolveAssetRoot(explicit string) (string, error) {
	if explicit != "" {
		if validAssetRoot(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("noVNC static assets not found at %s", explicit)
	}
	for _, root := range p.Options.AssetRoots {
		if validAssetRoot(root) {
			return root, nil
		}
	}
	return "", fmt.Errorf("noVNC static assets not found")
}

func validAssetRoot(root string) bool {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false
	}
	return fileExists(filepath.Join(root, "vnc.html")) || fileExists(filepath.Join(root, "index.html"))
}

func targetAddress(req Request) string {
	if req.TargetAddress != "" {
		return req.TargetAddress
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(defaultPort(req.VNCPort, 5900)))
}

func defaultPort(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func bridge(a net.Conn, b net.Conn) error {
	errCh := make(chan error, 2)
	go copyAndClose(errCh, a, b)
	go copyAndClose(errCh, b, a)
	err := <-errCh
	_ = a.Close()
	_ = b.Close()
	<-errCh
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func copyAndClose(errCh chan<- error, dst net.Conn, src net.Conn) {
	_, err := io.Copy(dst, src)
	errCh <- err
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
