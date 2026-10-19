// Package sites turns the folders in the project roots into sites (<folder>.test), each with a
// stable internal port, and watches the roots for new or removed folders.
package sites

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/text/unicode/norm"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/ports"
)

// Site is one local website.
type Site struct {
	Name    string   `json:"name"`     // "tienda-nueva"
	Host    string   `json:"host"`     // "tienda-nueva.test"
	Folder  string   `json:"folder"`   // C:\www\tienda_nueva
	DocRoot string   `json:"doc_root"` // …\public when there is one
	Kind    string   `json:"kind"`     // laravel, wordpress, symfony, php, static
	Aliases []string `json:"aliases,omitempty"`
	HTTPS   bool     `json:"https"`
	Server  string   `json:"server"`           // apache, nginx, frankenphp
	PHP     string   `json:"php,omitempty"`    // PHP version the site pinned ("" = the global one)
	Vite    bool     `json:"vite,omitempty"`   // has a Vite config and package.json (npm run dev)
	Reverb  bool     `json:"reverb,omitempty"` // requires laravel/reverb (WebSockets at reverb.<host>)
	Debug   bool     `json:"debug,omitempty"`  // runs with Xdebug
	// Missing lists what the project lacks to run here: "vendor" (Composer packages), ".env" and
	// "frontend" (the build Laravel's @vite reads). «Preparar» takes care of them.
	Missing []string `json:"missing,omitempty"`
	Port    int      `json:"port"` // internal port on its web server

	// Filled in by clon when it plans the PHP pools.
	PHPVersion string `json:"php_version,omitempty"` // the PHP that serves the site
	Pool       string `json:"-"`                     // name of its php-cgi pool
	ReverbPort int    `json:"reverb_port,omitempty"` // internal port of its Reverb server
}

// Warning explains why a folder did not become a site.
type Warning struct {
	Folder string `json:"folder"`
	Reason string `json:"reason"`
}

// Normalize maps a folder name to a valid host label: lower case, accents removed, spaces,
// underscores and dots turned into dashes ("mi proyecto 2" → "mi-proyecto-2").
func Normalize(folder string) (string, bool) {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(folder)) {
		switch {
		case unicode.Is(unicode.Mn, r): // combining accent marks
		case r == 'ñ':
			b.WriteRune('n')
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	return s, s != ""
}

// publicDirs are checked in order; the first that exists becomes the document root.
var publicDirs = []string{"public", "web", "webroot", "public_html"}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func detect(folder, override string) (docRoot, kind string) {
	switch {
	case exists(filepath.Join(folder, "artisan")):
		kind = "laravel"
	case exists(filepath.Join(folder, "wp-config.php")) || exists(filepath.Join(folder, "wp-load.php")):
		kind = "wordpress"
	case exists(filepath.Join(folder, "symfony.lock")) || exists(filepath.Join(folder, "bin", "console")):
		kind = "symfony"
	case exists(filepath.Join(folder, "index.php")) || exists(filepath.Join(folder, "composer.json")):
		kind = "php"
	default:
		kind = "static"
	}
	if override != "" {
		return filepath.Join(folder, override), kind
	}
	if kind == "wordpress" {
		return folder, kind
	}
	for _, d := range publicDirs {
		p := filepath.Join(folder, d)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			if kind == "static" {
				kind = "php"
			}
			return p, kind
		}
	}
	return folder, kind
}

// Missing lists what a project lacks to run here (see Site.Missing).
func Missing(folder, kind string) []string {
	var out []string
	if exists(filepath.Join(folder, "composer.json")) && !exists(filepath.Join(folder, "vendor", "autoload.php")) {
		out = append(out, "vendor")
	}
	if kind == "laravel" && !exists(filepath.Join(folder, ".env")) && exists(filepath.Join(folder, ".env.example")) {
		out = append(out, ".env")
	}
	if kind == "laravel" && hasVite(folder) && !exists(filepath.Join(folder, "public", "build", "manifest.json")) &&
		!exists(filepath.Join(folder, "public", "hot")) {
		out = append(out, "frontend")
	}
	return out
}

// HasVite reports whether a project runs a Vite dev server (npm run dev).
func HasVite(folder string) bool { return hasVite(folder) }

// hasReverb reports whether a Laravel project requires Reverb, Laravel's WebSocket server
// (php artisan install:broadcasting --reverb adds it).
func hasReverb(folder string) bool {
	data, err := os.ReadFile(filepath.Join(folder, "composer.json"))
	if err != nil {
		return false
	}
	var c struct {
		Require map[string]string `json:"require"`
	}
	if json.Unmarshal(data, &c) != nil {
		return false
	}
	_, ok := c.Require["laravel/reverb"]
	return ok
}

// lockFiles tell which package manager a project uses, as Laravel's installer reads them.
var lockFiles = []struct{ file, manager string }{
	{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"package-lock.json", "npm"},
}

// PackageManager is the JavaScript package manager of a project: the one package.json names in
// "packageManager" ("pnpm@10.11.0"), or the one whose lock file is there; npm otherwise.
func PackageManager(folder string) string {
	if data, err := os.ReadFile(filepath.Join(folder, "package.json")); err == nil {
		var pkg struct {
			PackageManager string `json:"packageManager"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			if name, _, _ := strings.Cut(pkg.PackageManager, "@"); name != "" {
				for _, l := range lockFiles {
					if l.manager == name {
						return name
					}
				}
			}
		}
	}
	for _, l := range lockFiles {
		if exists(filepath.Join(folder, l.file)) {
			return l.manager
		}
	}
	return "npm"
}

// LockFiles are the lock files of the package managers other than manager, which a project switched
// to manager no longer needs.
func LockFiles(except string) []string {
	var out []string
	for _, l := range lockFiles {
		if l.manager != except {
			out = append(out, l.file)
		}
	}
	return out
}

// hasVite reports whether a project runs a Vite dev server (npm run dev).
func hasVite(folder string) bool {
	if !exists(filepath.Join(folder, "package.json")) {
		return false
	}
	for _, name := range []string{"vite.config.js", "vite.config.ts", "vite.config.mjs", "vite.config.mts"} {
		if exists(filepath.Join(folder, name)) {
			return true
		}
	}
	return false
}

// Scan lists the sites of all roots. The first root wins when two folders map to the same name.
func Scan(cfg config.Config) ([]Site, []Warning) {
	var out []Site
	var warns []Warning
	seen := map[string]string{}
	for _, root := range cfg.Roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			warns = append(warns, Warning{Folder: root, Reason: err.Error()})
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			folder := filepath.Join(root, e.Name())
			name, ok := Normalize(e.Name())
			if !ok {
				warns = append(warns, Warning{Folder: folder, Reason: "el nombre no se puede convertir en dominio"})
				continue
			}
			if prev, dup := seen[name]; dup {
				warns = append(warns, Warning{Folder: folder, Reason: fmt.Sprintf("%s.%s ya corresponde a %s", name, cfg.TLD, prev)})
				continue
			}
			settings := cfg.Sites[name]
			if settings.Ignore {
				continue
			}
			seen[name] = folder
			docRoot, kind := detect(folder, settings.PublicDir)
			out = append(out, Site{
				Name:    name,
				Host:    name + "." + cfg.TLD,
				Folder:  folder,
				DocRoot: docRoot,
				Kind:    kind,
				Aliases: settings.Aliases,
				HTTPS:   cfg.HTTPSFor(name),
				Server:  cfg.ServerFor(name),
				PHP:     settings.PHP,
				Vite:    hasVite(folder),
				Reverb:  kind == "laravel" && hasReverb(folder),
				Debug:   settings.Debug,
				Missing: Missing(folder, kind),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warns
}

// PortStore keeps each site's internal port stable across restarts (state\site-ports.json).
type PortStore struct {
	path  string
	Ports map[string]int `json:"ports"`
}

func LoadPorts(path string) (*PortStore, error) {
	ps := &PortStore{path: path, Ports: map[string]int{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ps, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, ps); err != nil {
		return nil, err
	}
	if ps.Ports == nil {
		ps.Ports = map[string]int{}
	}
	return ps, nil
}

func (ps *PortStore) Save() error {
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(ps.path, data)
}

// Assign gives every site a port in [from, to]: its previous one when still usable, otherwise
// the first free one. ownPorts are ports currently held by clon's own web server (not conflicts).
func (ps *PortStore) Assign(list []Site, from, to int, ownPorts map[int]bool) error {
	taken := map[int]bool{}
	for i := range list {
		if p, ok := ps.Ports[list[i].Name]; ok && p >= from && p <= to && !taken[p] && (ownPorts[p] || ports.Check(p) == nil) {
			list[i].Port = p
			taken[p] = true
		}
	}
	for i := range list {
		if list[i].Port != 0 {
			continue
		}
		for p := range ps.Ports {
			taken[ps.Ports[p]] = true
		}
		p, err := ports.FirstFree(from, to, taken)
		if err != nil {
			return err
		}
		list[i].Port = p
		taken[p] = true
		ps.Ports[list[i].Name] = p
	}
	return nil
}

// Watch calls onChange (debounced) when folders appear or disappear in the roots.
func Watch(roots []string, onChange func()) (func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	for _, r := range roots {
		if err := w.Add(r); err != nil {
			w.Close()
			return nil, fmt.Errorf("no se puede vigilar %s: %w", r, err)
		}
	}
	var mu sync.Mutex
	var timer *time.Timer
	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				mu.Lock()
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(1500*time.Millisecond, onChange)
				mu.Unlock()
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return func() { w.Close() }, nil
}
