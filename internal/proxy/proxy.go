// Package proxy is the front HTTPS server. It owns the public ports (80/443), presents each site's
// certificate, redirects HTTP to HTTPS and forwards requests to the site's internal port.
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nicotc/laraclon/internal/ca"
)

// Route sends one host to a backend.
type Route struct {
	Site    string // site name, for messages
	Server  string // "Apache 2.4.62", for messages
	Backend *url.URL
	HTTPS   bool
}

// Table maps host names to routes. "*.site.test" matches through the parent site.
type Table struct {
	mu     sync.RWMutex
	routes map[string]Route
}

func (t *Table) Set(routes map[string]Route) {
	t.mu.Lock()
	t.routes = routes
	t.mu.Unlock()
}

func (t *Table) Lookup(host string) (Route, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	t.mu.RLock()
	defer t.mu.RUnlock()
	for h := host; h != ""; {
		if r, ok := t.routes[h]; ok {
			return r, true
		}
		_, rest, found := strings.Cut(h, ".")
		if !found || !strings.Contains(rest, ".") {
			break
		}
		h = rest
	}
	return Route{}, false
}

// Hosts returns the configured names, sorted.
func (t *Table) Hosts() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.routes))
	for h := range t.routes {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Server runs the HTTP and HTTPS listeners.
type Server struct {
	Table     *Table
	CA        *ca.Authority
	Panel     http.Handler // serves https://localhost
	TLD       string       // local top-level domain, e.g. "test"
	HTTPPort  int
	HTTPSPort int
	Addrs     []string // listen addresses, e.g. 127.0.0.1 and ::1

	mu      sync.Mutex
	servers []*http.Server
	proxies sync.Map // backend URL → *httputil.ReverseProxy
}

var transport = &http.Transport{
	Proxy:                 nil, // never send local traffic through a system proxy
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	MaxIdleConnsPerHost:   32,
	IdleConnTimeout:       90 * time.Second,
	DisableCompression:    true,
	ResponseHeaderTimeout: 0, // a request paused in Xdebug may take minutes
}

// Start opens the listeners. Errors are returned when a port cannot be bound.
func (s *Server) Start() error {
	tlsConf := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := strings.ToLower(hello.ServerName)
			switch {
			case name == "" || name == "localhost" || strings.HasSuffix(name, ".localhost"):
				return s.CA.Leaf("localhost")
			}
			if _, ok := s.Table.Lookup(name); ok {
				return s.CA.Leaf(certHost(name, s.Table))
			}
			// Unknown names in the local TLD still get a certificate, so the browser shows clon's
			// "no such site" page instead of a TLS error.
			if s.TLD != "" && strings.HasSuffix(name, "."+s.TLD) {
				return s.CA.Leaf(name)
			}
			return nil, fmt.Errorf("clon no conoce el sitio %q", name)
		},
	}
	var started []*http.Server
	fail := func(err error) error {
		for _, srv := range started {
			srv.Close()
		}
		return err
	}
	for _, addr := range s.Addrs {
		httpsLn, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(s.HTTPSPort)))
		if err != nil {
			return fail(fmt.Errorf("no se pudo abrir el puerto %d en %s: %w", s.HTTPSPort, addr, err))
		}
		httpLn, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(s.HTTPPort)))
		if err != nil {
			httpsLn.Close()
			return fail(fmt.Errorf("no se pudo abrir el puerto %d en %s: %w", s.HTTPPort, addr, err))
		}
		httpsSrv := &http.Server{Handler: http.HandlerFunc(s.serveHTTPS), TLSConfig: tlsConf, ReadHeaderTimeout: 30 * time.Second}
		httpSrv := &http.Server{Handler: http.HandlerFunc(s.serveHTTP), ReadHeaderTimeout: 30 * time.Second}
		go httpsSrv.ServeTLS(httpsLn, "", "")
		go httpSrv.Serve(httpLn)
		started = append(started, httpsSrv, httpSrv)
	}
	s.mu.Lock()
	s.servers = started
	s.mu.Unlock()
	return nil
}

// certHost picks the site host a certificate should be issued for (the parent of a subdomain).
func certHost(name string, t *Table) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for h := name; h != ""; {
		if _, ok := t.routes[h]; ok {
			return h
		}
		_, rest, found := strings.Cut(h, ".")
		if !found {
			break
		}
		h = rest
	}
	return name
}

// Stop closes the listeners.
func (s *Server) Stop(ctx context.Context) {
	s.mu.Lock()
	servers := s.servers
	s.servers = nil
	s.mu.Unlock()
	for _, srv := range servers {
		srv.Shutdown(ctx)
	}
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(hostport)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)
	if route, ok := s.Table.Lookup(host); ok && !route.HTTPS {
		s.forward(w, r, route)
		return
	}
	target := "https://" + host
	if s.HTTPSPort != 443 {
		target += ":" + strconv.Itoa(s.HTTPSPort)
	}
	http.Redirect(w, r, target+r.URL.RequestURI(), http.StatusMovedPermanently)
}

func (s *Server) serveHTTPS(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost") {
		if s.Panel != nil {
			s.Panel.ServeHTTP(w, r)
			return
		}
	}
	route, ok := s.Table.Lookup(host)
	if !ok {
		s.notFound(w, host)
		return
	}
	s.forward(w, r, route)
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request, route Route) {
	key := route.Backend.String()
	rp, ok := s.proxies.Load(key)
	if !ok {
		target := route.Backend
		rp, _ = s.proxies.LoadOrStore(key, &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.Host = pr.In.Host // the web server and PHP see the real host name
				pr.SetXForwarded()
				if pr.In.TLS != nil {
					pr.Out.Header.Set("X-Forwarded-Port", strconv.Itoa(s.HTTPSPort))
				} else {
					pr.Out.Header.Set("X-Forwarded-Port", strconv.Itoa(s.HTTPPort))
				}
			},
			Transport:     transport,
			FlushInterval: -1,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				s.backendDown(w, route, err)
			},
		})
	}
	rp.(*httputil.ReverseProxy).ServeHTTP(w, r)
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8"><title>{{.Title}}</title>
<style>
body{font:16px/1.5 system-ui,Segoe UI,sans-serif;max-width:720px;margin:10vh auto;padding:0 24px;color:#1f2328}
h1{font-size:22px} code{background:#f3f4f6;padding:2px 6px;border-radius:4px} a{color:#0969da}
.muted{color:#656d76;font-size:14px} li{margin:4px 0}
</style></head><body>
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
{{if .Detail}}<p class="muted"><code>{{.Detail}}</code></p>{{end}}
{{if .Links}}<p>Sitios con un nombre parecido:</p><ul>{{range .Links}}<li><a href="https://{{.}}{{$.PortSuffix}}/">{{.}}</a></li>{{end}}</ul>{{end}}
<p class="muted"><a href="https://localhost{{.PortSuffix}}/">Abrir el panel de clon</a></p>
</body></html>`))

func (s *Server) portSuffix() string {
	if s.HTTPSPort == 443 {
		return ""
	}
	return ":" + strconv.Itoa(s.HTTPSPort)
}

func (s *Server) backendDown(w http.ResponseWriter, route Route, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	pageTemplate.Execute(w, map[string]any{
		"Title":      route.Server + " no responde",
		"Message":    fmt.Sprintf("El servidor del sitio %s no está corriendo o no contesta. Inícialo desde el icono de clon en la bandeja o con «clon start».", route.Site),
		"Detail":     err.Error(),
		"PortSuffix": s.portSuffix(),
	})
}

func (s *Server) notFound(w http.ResponseWriter, host string) {
	name := strings.Split(host, ".")[0]
	var similar []string
	for _, h := range s.Table.Hosts() {
		if n := strings.Split(h, ".")[0]; strings.Contains(n, name) || strings.Contains(name, n) {
			similar = append(similar, h)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	pageTemplate.Execute(w, map[string]any{
		"Title":      "No hay ningún sitio llamado " + host,
		"Message":    "Crea una carpeta con ese nombre en una de tus carpetas de proyectos y clon la publicará sola.",
		"Links":      similar,
		"PortSuffix": s.portSuffix(),
	})
}
