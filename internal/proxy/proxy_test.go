package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestLocalOnlyToolsRefuseOtherComputersAndComeBeforeThePanel(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("adminer")) }))
	defer backend.Close()
	u, _ := url.Parse(backend.URL)
	var tb Table
	tb.Set(map[string]Route{"db.localhost": {Site: "db.localhost", Backend: u, HTTPS: true, LocalOnly: true}})
	panel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("panel")) })
	s := &Server{Table: &tb, Panel: panel, HTTPSPort: 443}

	get := func(host, remote string) (int, string) {
		r := httptest.NewRequest("GET", "https://"+host+"/", nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		s.serveHTTPS(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := get("db.localhost", "127.0.0.1:50000"); code != 200 || body != "adminer" {
		t.Errorf("from this computer: %d %q", code, body)
	}
	if code, body := get("db.localhost", "[::1]:50000"); code != 200 || body != "adminer" {
		t.Errorf("from ::1: %d %q", code, body)
	}
	if code, _ := get("db.localhost", "192.168.1.20:50000"); code != http.StatusForbidden {
		t.Errorf("from the LAN: %d, want 403", code)
	}
	if code, body := get("localhost", "127.0.0.1:50000"); code != 200 || body != "panel" {
		t.Errorf("localhost is still the panel: %d %q", code, body)
	}
}

func TestSharedLinkAsksForItsPassword(t *testing.T) {
	var seenAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Write([]byte(r.Host))
	}))
	defer backend.Close()
	u, _ := url.Parse(backend.URL)
	var tb Table
	tb.Set(map[string]Route{"abc-def.trycloudflare.com": {Site: "tienda.test", Backend: u, HTTPS: true, Password: "s3creto"}})
	s := &Server{Table: &tb, HTTPSPort: 443}
	get := func(pw string) (int, string) {
		r := httptest.NewRequest("GET", "https://abc-def.trycloudflare.com/", nil)
		if pw != "" {
			r.SetBasicAuth("quien-sea", pw)
		}
		w := httptest.NewRecorder()
		s.serveHTTPS(w, r)
		return w.Code, w.Body.String()
	}
	if code, _ := get(""); code != http.StatusUnauthorized {
		t.Errorf("without password: %d, want 401", code)
	}
	if code, _ := get("otra"); code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d, want 401", code)
	}
	code, body := get("s3creto")
	if code != 200 || body != "abc-def.trycloudflare.com" {
		t.Errorf("right password: %d %q (the site must see the public name)", code, body)
	}
	if seenAuth != "" {
		t.Errorf("the site saw Nido's password: %q", seenAuth)
	}
}

func TestLookupMatchesSubdomainsThroughTheSite(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:20001")
	var tb Table
	tb.Set(map[string]Route{"app.test": {Site: "app.test", Backend: u}})
	for _, host := range []string{"app.test", "APP.test.", "admin.app.test", "a.b.app.test"} {
		if _, ok := tb.Lookup(host); !ok {
			t.Errorf("Lookup(%q) should match app.test", host)
		}
	}
	for _, host := range []string{"otra.test", "test", "app.test.example"} {
		if _, ok := tb.Lookup(host); ok {
			t.Errorf("Lookup(%q) should not match", host)
		}
	}
	if got := certHost("admin.app.test", &tb); got != "app.test" {
		t.Errorf("certHost = %q, want app.test", got)
	}
}
