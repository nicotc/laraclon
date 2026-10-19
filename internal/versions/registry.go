// Package versions keeps the catalogue of installed components (PHP, Apache, MySQL, Node…).
package versions

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/nicotc/laraclon/internal/config"
)

type Component string

const (
	PHP      Component = "php"
	Apache   Component = "apache"
	MySQL    Component = "mysql"
	MariaDB  Component = "mariadb"
	Nginx    Component = "nginx"
	Node     Component = "node"
	Redis    Component = "redis"
	Composer Component = "composer"

	FrankenPHP Component = "frankenphp"
)

// Version is one installed (or referenced) copy of a component.
type Version struct {
	Component  Component `json:"component"`
	Version    string    `json:"version"`
	Path       string    `json:"path"`
	Source     string    `json:"source"` // download, laragon, import
	Arch       string    `json:"arch,omitempty"`
	ThreadSafe *bool     `json:"thread_safe,omitempty"`
	PHP        string    `json:"php,omitempty"` // PHP built into the program (FrankenPHP)
}

var componentNames = map[Component]string{PHP: "PHP", Apache: "Apache", MySQL: "MySQL", MariaDB: "MariaDB", Nginx: "Nginx", Node: "Node", Redis: "Redis", Composer: "Composer", FrankenPHP: "FrankenPHP"}

// Name is the product name shown to the user, e.g. "MariaDB".
func (c Component) Name() string {
	if n, ok := componentNames[c]; ok {
		return n
	}
	return string(c)
}

// Label is a short human description, e.g. "PHP 8.4.16 (NTS x64)".
func (v Version) Label() string {
	name := v.Component.Name()
	extra := []string{}
	if v.ThreadSafe != nil {
		if *v.ThreadSafe {
			extra = append(extra, "TS")
		} else {
			extra = append(extra, "NTS")
		}
	}
	if v.Arch != "" {
		extra = append(extra, v.Arch)
	}
	if v.PHP != "" {
		extra = append(extra, "PHP "+v.PHP)
	}
	s := name + " " + v.Version
	if len(extra) > 0 {
		s += " (" + strings.Join(extra, " ") + ")"
	}
	return s
}

// Registry is persisted as JSON in state\versions.json.
type Registry struct {
	mu    sync.Mutex
	path  string
	Items []Version `json:"versions"`
}

func Load(path string) (*Registry, error) {
	r := &Registry{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(r.path, data)
}

// Add registers v, replacing an entry with the same component and version.
func (r *Registry) Add(v Version) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, it := range r.Items {
		if it.Component == v.Component && it.Version == v.Version {
			r.Items[i] = v
			return
		}
	}
	r.Items = append(r.Items, v)
}

func (r *Registry) Remove(c Component, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.Items[:0]
	for _, it := range r.Items {
		if !(it.Component == c && it.Version == version) {
			out = append(out, it)
		}
	}
	r.Items = out
}

// List returns the versions of a component, newest first.
func (r *Registry) List(c Component) []Version {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Version
	for _, it := range r.Items {
		if it.Component == c {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return Compare(out[i].Version, out[j].Version) > 0 })
	return out
}

// Find resolves want against the installed versions of c. "8.4" matches the newest 8.4.x;
// an empty want returns the newest version.
func (r *Registry) Find(c Component, want string) (Version, bool) {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	for _, it := range r.List(c) {
		if want == "" || it.Version == want || strings.HasPrefix(it.Version, want+".") {
			return it, true
		}
	}
	return Version{}, false
}

// Compare compares dotted numeric versions ("8.4.16" vs "8.10.0"); non-numeric parts compare as text.
func Compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var sa, sb string
		if i < len(pa) {
			sa = pa[i]
		}
		if i < len(pb) {
			sb = pb[i]
		}
		na, ea := strconv.Atoi(sa)
		nb, eb := strconv.Atoi(sb)
		switch {
		case ea == nil && eb == nil && na != nb:
			if na < nb {
				return -1
			}
			return 1
		case (ea != nil || eb != nil) && sa != sb:
			if sa < sb {
				return -1
			}
			return 1
		}
	}
	return 0
}

// MajorMinor returns "8.4" for "8.4.16".
func MajorMinor(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}
