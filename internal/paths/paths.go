// Package paths describes the folder layout of a clon installation.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Home is the root folder of an installation. Everything clon owns lives under it.
type Home struct{ Root string }

// Resolve returns CLON_HOME when it is set, otherwise the folder of the running executable,
// which makes a copied installation portable.
func Resolve() (Home, error) {
	if h := os.Getenv("CLON_HOME"); h != "" {
		abs, err := filepath.Abs(h)
		if err != nil {
			return Home{}, err
		}
		return Home{Root: abs}, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return Home{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return Home{Root: filepath.Dir(exe)}, nil
}

func (h Home) join(base string, elem []string) string {
	return filepath.Join(append([]string{h.Root, base}, elem...)...)
}

func (h Home) Path(elem ...string) string      { return filepath.Join(append([]string{h.Root}, elem...)...) }
func (h Home) Bin(elem ...string) string       { return h.join("bin", elem) }
func (h Home) Data(elem ...string) string      { return h.join("data", elem) }
func (h Home) Config(elem ...string) string    { return h.join("config", elem) }
func (h Home) State(elem ...string) string     { return h.join("state", elem) }
func (h Home) Generated(elem ...string) string { return h.join("generated", elem) }
func (h Home) SSL(elem ...string) string       { return h.join("ssl", elem) }
func (h Home) Logs(elem ...string) string      { return h.join("logs", elem) }
func (h Home) Cache(elem ...string) string     { return h.join("cache", elem) }
func (h Home) Run(elem ...string) string       { return h.join("run", elem) }
func (h Home) Shims(elem ...string) string     { return h.join("shims", elem) }
func (h Home) Tmp(elem ...string) string       { return h.join("tmp", elem) }

// ConfigFile is the user-editable settings file.
func (h Home) ConfigFile() string { return h.Config("clon.toml") }

// Ensure creates the standard folder layout.
func (h Home) Ensure() error {
	if strings.ContainsAny(h.Root, " \t") {
		return fmt.Errorf("la carpeta de instalación no debe contener espacios: %s", h.Root)
	}
	for _, d := range []string{"bin", "data", "config", "state", "generated", "ssl", "logs", "cache", "run", "shims", "tmp"} {
		if err := os.MkdirAll(h.Path(d), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Slash converts a Windows path to forward slashes, the form Apache, PHP and MySQL configs expect.
func Slash(p string) string { return filepath.ToSlash(p) }
