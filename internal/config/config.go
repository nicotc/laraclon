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

	Proxy  Proxy  `toml:"proxy"`
	PHP    PHP    `toml:"php"`
	Node   Node   `toml:"node"`
	Apache Apache `toml:"apache"`
	Hosts  Hosts  `toml:"hosts"`

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
}

type Node struct {
	Version string `toml:"version"`
}

type Apache struct {
	Version string `toml:"version"`
}

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
