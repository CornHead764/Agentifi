package egress

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"
)

// ErrRefused is a connection the proxy would not make.
var ErrRefused = errors.New("egress: the address is not public")

// Proxy is a forward proxy on loopback whose every connection goes through
// Dial. A browser pointed at it opens no connection of its own, so a page, its
// redirects, its websockets and its service workers reach public addresses
// only. HTTPS passes through as an opaque tunnel, so the browser's own TLS is
// what the site sees.
type Proxy struct {
	listener net.Listener
	server   *http.Server
	dial     func(context.Context, string, string) (net.Conn, error)
	forward  *http.Transport

	// open is every tunnelled connection, which the server's Close does not
	// reach once hijacked.
	mu     sync.Mutex
	open   map[net.Conn]struct{}
	closed bool
}

// StartProxy listens on a free loopback port. A nil resolver is the system's.
func StartProxy(resolver *net.Resolver) (*Proxy, error) {
	return startProxy(dialWith(resolver, ErrRefused, Public))
}

func startProxy(dial func(context.Context, string, string) (net.Conn, error)) (*Proxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Proxy{listener: listener, dial: dial, open: map[net.Conn]struct{}{}}
	p.forward = &http.Transport{
		Proxy:                 nil,
		DialContext:           dial,
		ResponseHeaderTimeout: time.Minute,
		IdleConnTimeout:       time.Minute,
	}
	forward := &httputil.ReverseProxy{
		// The request is in absolute form, so its URL is already the target.
		Rewrite:   func(*httputil.ProxyRequest) {},
		Transport: p.forward,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.refuse(w, r.URL.Host, err)
		},
	}
	p.server = &http.Server{
		ReadHeaderTimeout: 30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				p.tunnel(w, r)
				return
			}
			if r.URL.Scheme != "http" || r.URL.Host == "" {
				http.Error(w, "egress: only an absolute http address is forwarded", http.StatusBadRequest)
				return
			}
			forward.ServeHTTP(w, r)
		}),
	}
	go func() { _ = p.server.Serve(listener) }()
	return p, nil
}

// URL is the address to hand a browser as its proxy server.
func (p *Proxy) URL() string { return "http://" + p.listener.Addr().String() }

// Close stops listening and drops every open tunnel.
func (p *Proxy) Close() error {
	err := p.server.Close()
	p.forward.CloseIdleConnections()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for conn := range p.open {
		_ = conn.Close()
	}
	clear(p.open)
	return err
}

// hold registers both ends of a tunnel, or says the proxy has closed.
func (p *Proxy) hold(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	for _, conn := range conns {
		p.open[conn] = struct{}{}
	}
	return true
}

func (p *Proxy) drop(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
		delete(p.open, conn)
	}
}

func (p *Proxy) refuse(w http.ResponseWriter, host string, err error) {
	if errors.Is(err, ErrRefused) {
		slog.Warn("the browser was kept from a non-public address", "host", host, "error", err)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	http.Error(w, "egress: "+err.Error(), http.StatusBadGateway)
}

// tunnel answers CONNECT: the target is dialled through the check, then bytes
// pass both ways untouched.
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := p.dial(r.Context(), "tcp", r.Host)
	if err != nil {
		p.refuse(w, r.Host, err)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "egress: the connection cannot be tunnelled", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if !p.hold(client, upstream) {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		p.drop(client, upstream)
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, buffered)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()
	go func() {
		<-done
		p.drop(client, upstream)
	}()
}
