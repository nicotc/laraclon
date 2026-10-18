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
	MySQL  MySQL  `toml:"mysql"`
	Hosts  Hosts  `toml:"hosts"`

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

type MySQL struct {
	Enabled bool   `toml:"enabled"`
	Version string `toml:"version"`
	Port    int    `toml:"port"`
	// DataDir uses an existing data folder (for example Laragon's) instead of clon's own.
	// DataDirVersion is the MySQL version that folder belongs to; clon refuses to open it with
	// another version, because MySQL would upgrade it in place.
	DataDir        string `toml:"data_dir,omitempty"`
	DataDirVersion string `toml:"data_dir_version,omitempty"`
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
		MySQL:  MySQL{Enabled: true, Port: 3306},
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
