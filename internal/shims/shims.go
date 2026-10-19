// Package shims maintains the shims\ folder: small launchers (php.exe, node.exe, npm.exe…) that
// always run the active version, so one PATH entry is enough for every terminal and editor.
package shims

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
)

// Target says what a shim name runs. Env values are set only when the caller has not set them;
// ForceEnv values always win (PHPRC must point to Nido's php.ini so that child PHP processes
// started by Composer or Artisan load the same configuration). PathPrepend goes first in the
// child's PATH, so scripts it runs (npm run, composer scripts) find Nido's node, php and composer.
type Target struct {
	Exe         string            `json:"exe"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	ForceEnv    map[string]string `json:"force_env,omitempty"`
	PathPrepend []string          `json:"path_prepend,omitempty"`
	// Dirs replace Exe, Args and ForceEnv when the command runs inside one of their folders: the
	// projects that pinned their own PHP get it in the terminal too.
	Dirs []DirTarget `json:"dirs,omitempty"`
}

// DirTarget is what a shim runs inside one folder (and its subfolders).
type DirTarget struct {
	Dir      string            `json:"dir"`
	Exe      string            `json:"exe"`
	Args     []string          `json:"args,omitempty"`
	ForceEnv map[string]string `json:"force_env,omitempty"`
}

// TargetsFile is read by nido-shim.exe at every invocation.
const TargetsFile = "targets.json"

// Write stores targets and makes sure there is one launcher per name, copied from template
// (nido-shim.exe). Launchers whose name is no longer a target are removed.
func Write(home paths.Home, template string, targets map[string]Target) error {
	dir := home.Shims()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return err
	}
	if err := config.WriteFileAtomic(filepath.Join(dir, TargetsFile), data); err != nil {
		return err
	}
	want, err := fileHash(template)
	if err != nil {
		return err
	}
	for name := range targets {
		dst := filepath.Join(dir, name+".exe")
		if h, err := fileHash(dst); err == nil && bytes.Equal(h, want) {
			continue
		}
		if err := install(template, dst); err != nil {
			return err
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if !strings.HasSuffix(n, ".exe") {
			continue
		}
		if _, ok := targets[strings.TrimSuffix(n, ".exe")]; !ok {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// install hard-links the template (no extra disk space) or copies it when linking is not possible.
func install(template, dst string) error {
	os.Remove(dst)
	if err := os.Link(template, dst); err == nil {
		return nil
	}
	in, err := os.Open(template)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fileHash(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
