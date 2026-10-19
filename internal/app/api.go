package app

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/nicotc/laraclon/internal/hosts"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

//go:embed panel
var panelFiles embed.FS

var panelPage = template.Must(template.ParseFS(panelFiles, "panel/index.html"))

// Status is what the panel, the tray and `clon status` show.
type Status struct {
	Home           string              `json:"home"`
	PHP            string              `json:"php"`
	PHPVersions    []string            `json:"php_versions"`
	Node           string              `json:"node"`
	Apache         string              `json:"apache"`
	ApacheVersions []string            `json:"apache_versions"`
	Nginx          string              `json:"nginx"` // version the Nginx sites use ("" when not installed)
	FrankenPHP     string              `json:"frankenphp"`
	FrankenPHPPHP  string              `json:"frankenphp_php"` // the PHP built into FrankenPHP
	DB             string              `json:"db"`             // the default database, e.g. "MySQL 8.4.11"
	DBPort         int                 `json:"db_port"`
	Databases      []DBGroup           `json:"databases"`
	Jobs           []Job               `json:"jobs"`
	PanelURL       string              `json:"panel_url"`
	ProxyUp        bool                `json:"proxy_up"`
	HTTPPort       int                 `json:"http_port"`
	HTTPSPort      int                 `json:"https_port"`
	CATrusted      bool                `json:"ca_trusted"`
	Runtime        RuntimeStatus       `json:"runtime"` // Microsoft's Visual C++ runtime
	CACert         string              `json:"ca_cert"`
	HostsMissing   []string            `json:"hosts_missing"`
	Services       []supervisor.Status `json:"services"`
	Sites          []SiteStatus        `json:"sites"`
	Warnings       []sites.Warning     `json:"warnings"`
	Problems       map[string]string   `json:"problems"`
}

// SiteStatus is one site as shown to the user.
type SiteStatus struct {
	sites.Site
	URL string `json:"url"`
}

// Status collects the current state.
func (d *Daemon) Status() Status {
	st := Status{
		Home: d.env.Home.Root, PanelURL: d.PanelURL(),
		HTTPPort: d.env.Cfg.Proxy.HTTPPort, HTTPSPort: d.env.Cfg.Proxy.HTTPSPort,
		CATrusted: d.CATrusted(), CACert: d.ca.CertPath(),
		Services: d.sup.Statuses(), Jobs: d.Jobs(), Runtime: d.runtimeStatus(),
	}
	// The runtime can change outside clon ('clon vcredist install', a restart of Windows).
	d.setProblem("visual-c++", d.runtimeProblem())
	if v, err := d.env.GlobalPHP(); err == nil {
		st.PHP = v.Version
	}
	for _, v := range d.env.Reg.List(versions.PHP) {
		st.PHPVersions = append(st.PHPVersions, v.Version)
	}
	if v, err := d.env.GlobalNode(); err == nil {
		st.Node = v.Version
	}
	if v, err := d.apacheVersion(); err == nil {
		st.Apache = v.Version
	}
	for _, v := range d.env.Reg.List(versions.Apache) {
		st.ApacheVersions = append(st.ApacheVersions, v.Version)
	}
	if v, err := d.nginxVersion(); err == nil {
		st.Nginx = v.Version
	}
	if v, err := d.frankenVersion(); err == nil {
		st.FrankenPHP, st.FrankenPHPPHP = v.Version, v.PHP
	}
	if db, ok := d.env.DefaultDB(); ok {
		st.DB, st.DBPort = db.Kind.Name()+" "+db.Version.Version, db.Instance.Port
	}
	d.mu.Lock()
	st.ProxyUp = d.proxyUp
	for _, s := range d.sites {
		st.Sites = append(st.Sites, SiteStatus{Site: s, URL: d.SiteURL(s)})
	}
	st.Warnings = append(st.Warnings, d.warnings...)
	st.Problems = map[string]string{}
	for k, v := range d.problems {
		st.Problems[k] = v
	}
	d.mu.Unlock()
	st.Databases = d.dbGroups(st.Problems)
	if content, err := os.ReadFile(hosts.Path()); err == nil {
		st.HostsMissing = hosts.Missing(string(content), d.hostNames())
	}
	return st
}

// Handler serves the panel (https://localhost) and the JSON API used by the panel and the CLI.
// Requests must name localhost or 127.0.0.1 as host (blocks DNS rebinding) and API calls must carry
// the session token, which a page from another origin cannot read or send (blocks CSRF).
func (d *Daemon) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		panelPage.Execute(w, map[string]string{"Token": d.token})
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(mustSub("panel"))))

	api := func(pattern string, fn func(r *http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Clon-Token") != d.token {
				http.Error(w, "token no válido", http.StatusForbidden)
				return
			}
			out, err := fn(r)
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(out)
		})
	}
	api("GET /api/status", func(*http.Request) (any, error) { return d.Status(), nil })
	api("POST /api/start", func(*http.Request) (any, error) { d.StartAll(); return d.Status(), nil })
	api("POST /api/stop", func(*http.Request) (any, error) { d.StopAll(); return d.Status(), nil })
	api("POST /api/restart", func(*http.Request) (any, error) { d.StopAll(); d.StartAll(); return d.Status(), nil })
	api("POST /api/rescan", func(*http.Request) (any, error) { return d.Status(), d.Rescan() })
	api("POST /api/php", func(r *http.Request) (any, error) {
		var req struct{ Version string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		v, err := d.UsePHP(req.Version)
		return map[string]string{"php": v.Version}, err
	})
	api("POST /api/apache", func(r *http.Request) (any, error) {
		var req struct{ Version string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		v, err := d.UseApache(req.Version)
		return map[string]string{"apache": v.Version}, err
	})
	api("POST /api/vcredist/install", func(r *http.Request) (any, error) {
		err := d.InstallRuntime(r.Context())
		return d.Status(), err
	})
	api("POST /api/hosts/sync", func(*http.Request) (any, error) { return d.Status(), d.SyncHosts() })
	api("POST /api/ca/trust", func(*http.Request) (any, error) { return d.Status(), d.TrustCA() })
	api("POST /api/shutdown", func(*http.Request) (any, error) { go d.Shutdown(); return map[string]bool{"ok": true}, nil })

	// Databases: catalog, installs and one action per instance ("mysql-8.4").
	keyAction := func(fn func(string) error) func(*http.Request) (any, error) {
		return func(r *http.Request) (any, error) {
			var req struct{ Key string }
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return nil, err
			}
			err := fn(req.Key)
			return d.Status(), err
		}
	}
	api("GET /api/catalog", func(r *http.Request) (any, error) {
		q := r.URL.Query()
		return d.Catalog(r.Context(), versions.Component(q.Get("kind")), q.Get("refresh") == "1")
	})
	api("POST /api/services/install", func(r *http.Request) (any, error) {
		var req struct{ Kind, Version string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		return d.InstallService(versions.Component(req.Kind), req.Version)
	})
	api("POST /api/services/start", keyAction(d.StartService))
	api("POST /api/services/stop", keyAction(d.StopService))
	api("POST /api/services/default", keyAction(d.SetDefaultService))
	api("POST /api/services/uninstall", keyAction(d.UninstallService))
	api("POST /api/services/settings", func(r *http.Request) (any, error) {
		var req struct {
			Key       string
			Port      *int
			Autostart *bool
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		err := d.ServiceSettings(req.Key, req.Port, req.Autostart)
		return d.Status(), err
	})
	api("POST /api/services/open", func(r *http.Request) (any, error) {
		var req struct{ Key, What string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, d.OpenPath(req.Key, req.What)
	})
	api("POST /api/sites/server", func(r *http.Request) (any, error) {
		var req struct{ Site, Server string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		err := d.SetSiteServer(req.Site, req.Server)
		return d.Status(), err
	})
	api("GET /api/jobs", func(*http.Request) (any, error) { return d.Jobs(), nil })
	api("POST /api/jobs/cancel", func(r *http.Request) (any, error) {
		var req struct{ ID string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, d.CancelJob(req.ID)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		host = strings.Trim(strings.ToLower(host), "[]")
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "host no permitido", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !allowedOrigin(origin) {
			http.Error(w, "origen no permitido", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func allowedOrigin(origin string) bool {
	for _, prefix := range []string{"https://localhost", "http://127.0.0.1", "https://127.0.0.1"} {
		if origin == prefix || strings.HasPrefix(origin, prefix+":") {
			return true
		}
	}
	return false
}

func mustSub(dir string) fs.FS {
	sub, err := fs.Sub(panelFiles, dir)
	if err != nil {
		panic(fmt.Sprintf("panel: %v", err))
	}
	return sub
}
