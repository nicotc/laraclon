// Package config loads and saves the user settings file (config\clon.toml).
package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	TLD   string   `toml:"tld"`
	Roots []string `toml:"roots"`

	Proxy      Proxy      `toml:"proxy"`
	PHP        PHP        `toml:"php"`
	Node       Node       `toml:"node"`
	Apache     Apache     `toml:"apache"`
	Nginx      Nginx      `toml:"nginx"`
	FrankenPHP FrankenPHP `toml:"frankenphp"`
	Hosts      Hosts      `toml:"hosts"`

	// Services holds the databases by kind ("mysql", "mariadb").
	Services map[string]Service `toml:"services,omitempty"`

	// MySQL is the single-server section written by earlier versions of clon. It is read once and
	// moved into Services (see app.Env), then no longer written.
	MySQL *LegacyMySQL `toml:"mysql,omitempty"`

	Sites map[string]SiteSettings `toml:"sites,omitempty"`
}

// Proxy is the front HTTPS proxy that owns the public ports for every site.
type Proxy struct {
	HTTPPort  int  `toml:"http_port"`
	HTTPSPort int  `toml:"https_port"`
	LAN       bool `toml:"lan"`
}

// PHP holds the global PHP version and its FastCGI pool.
type PHP struct {
	Version  string `toml:"version"`
	PoolSize int    `toml:"pool_size"`
	BasePort int    `toml:"base_port"`
	// Extensions changes clon's default extensions per series ("8.4").
	Extensions map[string]PHPExtensions `toml:"extensions,omitempty"`
}

// PHPExtensions are the extensions switched on or off in one PHP series, against clon's defaults.
type PHPExtensions struct {
	Enable  []string `toml:"enable,omitempty"`
	Disable []string `toml:"disable,omitempty"`
}

// SetExtension switches an extension on or off for a PHP series. isDefault says whether clon
// enables it on its own, so only real changes are written.
func (p *PHP) SetExtension(series, name string, on, isDefault bool) {
	if p.Extensions == nil {
		p.Extensions = map[string]PHPExtensions{}
	}
	x := p.Extensions[series]
	x.Enable, x.Disable = without(x.Enable, name), without(x.Disable, name)
	switch {
	case on && !isDefault:
		x.Enable = append(x.Enable, name)
	case !on && isDefault:
		x.Disable = append(x.Disable, name)
	}
	if len(x.Enable) == 0 && len(x.Disable) == 0 {
		delete(p.Extensions, series)
		return
	}
	p.Extensions[series] = x
}

func without(list []string, s string) []string {
	var out []string
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

type Node struct {
	Version string `toml:"version"`
}

type Apache struct {
	Version string `toml:"version"`
}

// Nginx is the version used by the sites that choose Nginx ("" = newest installed).
type Nginx struct {
	Version string `toml:"version"`
}

// FrankenPHP is the version used by the sites that choose FrankenPHP ("" = newest installed).
type FrankenPHP struct {
	Version string `toml:"version"`
}

// Web servers a site can choose.
const (
	ServerApache     = "apache"
	ServerNginx      = "nginx"
	ServerFrankenPHP = "frankenphp"
)

// LegacyMySQL is the [mysql] section of earlier versions: one server with one data folder.
type LegacyMySQL struct {
	Enabled        bool   `toml:"enabled"`
	Version        string `toml:"version"`
	Port           int    `toml:"port"`
	DataDir        string `toml:"data_dir,omitempty"`
	DataDirVersion string `toml:"data_dir_version,omitempty"`
}

// Service lists the instances of one kind of service, one per series ("8.4"). The default one
// uses the standard port, always starts with «Iniciar todo» and is what the `mysql` shortcut opens.
type Service struct {
	Default   string                     `toml:"default,omitempty"`
	Instances map[string]ServiceInstance `toml:"instances,omitempty"`
}

// ServiceInstance is one series of a service. It runs the newest installed version of the series
// unless Version pins one. DataDir points to existing data (Laragon's, for example) instead of
// clon's own data\<kind>-<series>; such a folder is only ever opened with the version that owns
// it, because MySQL upgrades data in place and the older version could no longer read it.
type ServiceInstance struct {
	Port      int    `toml:"port"`
	Autostart bool   `toml:"autostart,omitempty"`
	Version   string `toml:"version,omitempty"`
	DataDir   string `toml:"data_dir,omitempty"`
}

// Service returns the settings of one kind of service (empty when there are none).
func (c *Config) Service(kind string) Service {
	s := c.Services[kind]
	if s.Instances == nil {
		s.Instances = map[string]ServiceInstance{}
	}
	return s
}

// SetService stores the settings of one kind of service.
func (c *Config) SetService(kind string, s Service) {
	if c.Services == nil {
		c.Services = map[string]Service{}
	}
	c.Services[kind] = s
}

// Hosts controls the managed block in the Windows hosts file.
type Hosts struct {
	Manage bool `toml:"manage"`
}

// SiteSettings are per-site overrides, keyed by the site name (the part before .test).
type SiteSettings struct {
	Aliases   []string `toml:"aliases,omitempty"`
	PublicDir string   `toml:"public_dir,omitempty"`
	HTTPS     *bool    `toml:"https,omitempty"`
	Ignore    bool     `toml:"ignore,omitempty"`
	Server    string   `toml:"server,omitempty"` // "nginx" or "frankenphp"; empty means Apache
	// PHP pins the site to a PHP version ("7.2") instead of the global one: the one exception to the
	// global PHP, meant for older projects. Its requests go to a pool of php-cgi of that version.
	PHP string `toml:"php,omitempty"`
	// Processes start with clon's services: "vite" (npm run dev), "queue", "schedule".
	Processes []string `toml:"processes,omitempty"`
}

func (s SiteSettings) empty() bool {
	return len(s.Aliases) == 0 && s.PublicDir == "" && s.HTTPS == nil && !s.Ignore && s.Server == "" && s.PHP == "" && len(s.Processes) == 0
}

// SetSiteProcess marks whether a project process starts with clon's services.
func (c *Config) SetSiteProcess(site, id string, on bool) {
	c.updateSite(site, func(s *SiteSettings) {
		s.Processes = without(s.Processes, id)
		if on {
			s.Processes = append(s.Processes, id)
		}
	})
}

// ServerFor is the web server of a site: Apache unless the site chose another.
func (c Config) ServerFor(site string) string {
	if s := c.Sites[site].Server; s != "" {
		return s
	}
	return ServerApache
}

// SetSiteServer stores a site's web server (Apache is the default and is not written).
func (c *Config) SetSiteServer(site, server string) {
	c.updateSite(site, func(s *SiteSettings) {
		s.Server = server
		if server == ServerApache {
			s.Server = ""
		}
	})
}

// SetSitePHP pins a site's PHP version; "" goes back to the global PHP.
func (c *Config) SetSitePHP(site, version string) {
	c.updateSite(site, func(s *SiteSettings) { s.PHP = version })
}

// updateSite changes one site's settings and drops the entry when nothing is left in it.
func (c *Config) updateSite(site string, fn func(*SiteSettings)) {
	if c.Sites == nil {
		c.Sites = map[string]SiteSettings{}
	}
	s := c.Sites[site]
	fn(&s)
	if s.empty() {
		delete(c.Sites, site)
		return
	}
	c.Sites[site] = s
}

func Defaults() Config {
	return Config{
		TLD:    "test",
		Proxy:  Proxy{HTTPPort: 80, HTTPSPort: 443},
		PHP:    PHP{PoolSize: 4, BasePort: 9100},
		Hosts:  Hosts{Manage: true},
		Apache: Apache{},
	}
}

// Load reads the settings file; missing keys keep their defaults and a missing file yields the defaults.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	return cfg, nil
}

// Save writes the settings atomically.
func Save(path string, cfg Config) error {
	var buf bytes.Buffer
	buf.WriteString("# Ajustes de clon. Puedes editarlo a mano; clon lo vuelve a leer al arrancar.\n\n")
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	return WriteFileAtomic(path, buf.Bytes())
}

// WriteFileAtomic writes data to a temporary file next to path and renames it into place.
// Unchanged files are left alone. The rename is retried because antivirus scanners often hold a
// freshly written file open for a moment, which makes Windows refuse the replacement.
func WriteFileAtomic(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(50+attempt*50) * time.Millisecond)
	}
	os.Remove(tmp)
	return err
}

// HTTPSFor reports whether a site is served over HTTPS (the default).
func (c Config) HTTPSFor(site string) bool {
	if s, ok := c.Sites[site]; ok && s.HTTPS != nil {
		return *s.HTTPS
	}
	return true
}
