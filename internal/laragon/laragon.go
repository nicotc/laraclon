// Package laragon imports an existing Laragon installation: its binaries are referenced in place
// (nothing is copied or modified) and its project roots are added to clon.
package laragon

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/versions"
)

// DefaultRoot is where the Laragon installer puts it.
const DefaultRoot = `C:\laragon`

// Report lists what an import found.
type Report struct {
	Versions []versions.Version
	Roots    []string
	Skipped  []string
	Defaults map[versions.Component]string
	// MissingRuntime is set when some programs could not start for lack of the Visual C++ runtime.
	MissingRuntime bool
}

// ini is a minimal reader for laragon.ini (sections and key=value lines).
type ini map[string]map[string]string

func readINI(path string) (ini, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := ini{}
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.ToLower(strings.Trim(line, "[]"))
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			if out[section] == nil {
				out[section] = map[string]string{}
			}
			out[section][strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return out, sc.Err()
}

// Import scans root\bin and root\usr\laragon.ini. Detected versions are added to reg, project roots
// and default versions to cfg (only where cfg has no value yet).
func Import(root string, reg *versions.Registry, cfg *config.Config) (Report, error) {
	rep := Report{Defaults: map[versions.Component]string{}}
	if _, err := os.Stat(filepath.Join(root, "bin")); err != nil {
		return rep, fmt.Errorf("no parece una instalación de Laragon: %s", root)
	}

	folders := map[string]versions.Component{
		"php": versions.PHP, "apache": versions.Apache, "mysql": versions.MySQL,
		"nginx": versions.Nginx, "nodejs": versions.Node, "redis": versions.Redis,
	}
	byPath := map[string]versions.Version{}
	for folder, comp := range folders {
		entries, _ := os.ReadDir(filepath.Join(root, "bin", folder))
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, "bin", folder, e.Name())
			v, err := versions.Detect(comp, dir)
			if err != nil {
				if errors.Is(err, procutil.ErrMissingDLL) {
					rep.MissingRuntime = true
					rep.Skipped = append(rep.Skipped, dir+": necesita el runtime de Visual C++")
				} else {
					rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", dir, err))
				}
				continue
			}
			v.Source = "laragon"
			reg.Add(v)
			rep.Versions = append(rep.Versions, v)
			byPath[strings.ToLower(filepath.Join(root, "bin", folder, e.Name()))] = v
		}
	}

	data, err := readINI(filepath.Join(root, "usr", "laragon.ini"))
	if err == nil {
		// The active document root goes first: it wins when a folder name exists in several roots.
		var roots []string
		if dr := data["apache"]["documentroot"]; dr != "" {
			roots = append(roots, dr)
		}
		if list := data["preferences"]["documentrootlist"]; list != "" {
			roots = append(roots, strings.Split(list, "|")...)
		}
		for _, r := range roots {
			r = filepath.Clean(strings.TrimSpace(r))
			if r == "." || r == "" {
				continue
			}
			if st, err := os.Stat(r); err != nil || !st.IsDir() {
				continue
			}
			if addRoot(cfg, r) {
				rep.Roots = append(rep.Roots, r)
			}
		}

		// Laragon's active versions become clon's defaults when clon has none yet.
		pick := func(section, folder string) (versions.Version, bool) {
			name := data[section]["version"]
			if name == "" {
				return versions.Version{}, false
			}
			v, ok := byPath[strings.ToLower(filepath.Join(root, "bin", folder, name))]
			return v, ok
		}
		if v, ok := pick("php", "php"); ok && cfg.PHP.Version == "" {
			cfg.PHP.Version = v.Version
			rep.Defaults[versions.PHP] = v.Version
		}
		// Laragon's active MySQL becomes clon's default MySQL series (the one on port 3306).
		if v, ok := pick("mysql", "mysql"); ok && cfg.MySQL == nil {
			if svc := cfg.Service(string(versions.MySQL)); svc.Default == "" && len(svc.Instances) == 0 {
				svc.Default = versions.MajorMinor(v.Version)
				cfg.SetService(string(versions.MySQL), svc)
				rep.Defaults[versions.MySQL] = v.Version
			}
		}
		if v, ok := pick("nodejs", "nodejs"); ok && cfg.Node.Version == "" {
			cfg.Node.Version = v.Version
			rep.Defaults[versions.Node] = v.Version
		}
		if cfg.Apache.Version == "" {
			if v, ok := reg.Find(versions.Apache, ""); ok {
				cfg.Apache.Version = v.Version
				rep.Defaults[versions.Apache] = v.Version
			}
		}
	}
	return rep, nil
}

func addRoot(cfg *config.Config, root string) bool {
	for _, r := range cfg.Roots {
		if strings.EqualFold(filepath.Clean(r), root) {
			return false
		}
	}
	cfg.Roots = append(cfg.Roots, root)
	return true
}
