package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

// NodeStatus is what the panel's Node card shows.
type NodeStatus struct {
	Global    string               `json:"global"`
	Installed []NodeInstalled      `json:"installed"`
	Lines     []install.Release    `json:"lines"`   // newest release of each line worth installing, newest first
	Updates   []install.NodeUpdate `json:"updates"` // for the Node in use
	Tools     []NodeTool           `json:"tools"`   // pnpm and yarn
	Error     string               `json:"error,omitempty"`
}

// NodeInstalled is one installed Node version.
type NodeInstalled struct {
	Version   string `json:"version"`
	Source    string `json:"source"` // download, import…
	Path      string `json:"path"`
	InUse     bool   `json:"in_use,omitempty"`    // the global one
	Removable bool   `json:"removable,omitempty"` // Nido downloaded it, so it can delete it
}

// NodeTool is a package manager installed next to npm.
type NodeTool struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"` // "" when it is not installed
}

// NodeStatus collects the Node card; refresh asks nodejs.org for its release list again.
func (d *Daemon) NodeStatus(ctx context.Context, refresh bool) NodeStatus {
	st := NodeStatus{Installed: []NodeInstalled{}, Lines: []install.Release{}, Updates: []install.NodeUpdate{}}
	global, gerr := d.env.GlobalNode()
	if gerr == nil {
		st.Global = global.Version
	}
	for _, v := range d.env.Reg.List(versions.Node) {
		st.Installed = append(st.Installed, NodeInstalled{Version: v.Version, Source: v.Source, Path: v.Path,
			InUse: gerr == nil && v.Version == global.Version, Removable: d.ownsNode(v)})
	}
	for _, name := range install.NodeTools {
		version, _, _ := install.NodePackage(d.env.NPMPrefix(), name)
		st.Tools = append(st.Tools, NodeTool{Name: name, Version: version})
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rels, err := install.NodeIndex(ctx, d.env.Home, refresh)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	for _, r := range install.NodeLines(rels, time.Now()) {
		st.Lines = append(st.Lines, r.Release())
	}
	st.Updates = append(st.Updates, d.nodeUpdates(rels)...)
	return st
}

// nodeUpdates compares the global Node with nodejs.org's list and keeps the result for Status.
func (d *Daemon) nodeUpdates(rels []install.NodeRelease) []install.NodeUpdate {
	var ups []install.NodeUpdate
	if global, err := d.env.GlobalNode(); err == nil {
		var have []string
		for _, v := range d.env.Reg.List(versions.Node) {
			have = append(have, v.Version)
		}
		ups = install.NodeUpdates(rels, global.Version, have)
	}
	d.mu.Lock()
	changed := fmt.Sprint(d.nodeNews) != fmt.Sprint(ups)
	d.nodeNews = ups
	d.mu.Unlock()
	if changed {
		d.changed()
	}
	return ups
}

// checkNodeNews recomputes the updates from the release list nodejs.org gave last (asking it again
// only when that list is a few hours old).
func (d *Daemon) checkNodeNews(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if rels, err := install.NodeIndex(ctx, d.env.Home, false); err == nil {
		d.nodeUpdates(rels)
	}
}

// watchNodeReleases looks for Node updates a little after Nido starts and then every hour; nodejs.org
// itself is asked at most every few hours (NodeIndex keeps its list).
func (d *Daemon) watchNodeReleases(ctx context.Context) {
	wait := 20 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = time.Hour
		d.checkNodeNews(ctx)
	}
}

// InstallNode starts downloading a Node version: "lts", "latest", a line ("24") or an exact version.
// With use it becomes the global Node once installed; the first one Nido downloads always does.
func (d *Daemon) InstallNode(want string, use bool) (Job, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rels, err := install.NodeIndex(ctx, d.env.Home, false)
	if err != nil {
		return Job{}, err
	}
	rel, err := install.ResolveNode(rels, want)
	if err != nil {
		return Job{}, err
	}
	return d.startInstall(rel.Release(), use)
}

// UseNode makes an installed Node the global one (node, npm and npx in terminals, project builds);
// the Vite servers that are running restart with it.
func (d *Daemon) UseNode(want string) (versions.Version, error) {
	v, ok := d.env.Reg.Find(versions.Node, want)
	if !ok {
		return v, fmt.Errorf("Node %s no está instalado (mira 'nido node list')", want)
	}
	d.cfgMu.Lock()
	d.env.Cfg.Node.Version = v.Version
	err := d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return v, err
	}
	d.refreshShims()
	d.restartNodeProcesses()
	go d.checkNodeNews(context.Background())
	d.changed()
	return v, nil
}

// ownsNode reports whether a Node version is a folder Nido downloaded into bin\node, which it may
// delete; versions found elsewhere are only forgotten.
func (d *Daemon) ownsNode(v versions.Version) bool {
	rel, err := filepath.Rel(d.env.Home.Bin("node"), v.Path)
	return v.Source == "download" && err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// UninstallNode deletes a Node version Nido downloaded, or forgets one it found elsewhere without
// touching its files. The global one stays.
func (d *Daemon) UninstallNode(version string) error {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	v, ok := d.env.Reg.Find(versions.Node, version)
	if !ok || v.Version != version {
		return fmt.Errorf("Node %s no está instalado (mira 'nido node list')", version)
	}
	if g, err := d.env.GlobalNode(); err == nil && g.Version == v.Version {
		return fmt.Errorf("Node %s es el global: elige otro antes de quitarlo", v.Version)
	}
	if d.ownsNode(v) {
		if err := os.RemoveAll(v.Path); err != nil {
			return fmt.Errorf("no se pudo borrar %s (¿lo está usando alguna terminal?): %w", v.Path, err)
		}
	}
	d.cfgMu.Lock()
	d.env.Reg.Remove(versions.Node, v.Version)
	err := d.env.SaveRegistry()
	d.cfgMu.Unlock()
	go d.checkNodeNews(context.Background())
	d.changed()
	return err
}

// SetSiteNode pins a site's Node version ("" or "global" goes back to the global Node): its Vite
// server, its frontend builds and node, npm, npx, pnpm and yarn inside its folder use it.
func (d *Daemon) SetSiteNode(site, want string) error {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	if strings.EqualFold(want, "global") {
		want = ""
	}
	s, err := d.findSite(site)
	if err != nil {
		return err
	}
	if want != "" {
		if _, ok := d.env.Reg.Find(versions.Node, want); !ok {
			return fmt.Errorf("Node %s no está instalado: instálalo en «PHP y Node» o con 'nido node install %s'", want, want)
		}
	}
	d.cfgMu.Lock()
	d.env.Cfg.SetSiteNode(s.Name, want)
	err = d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return err
	}
	d.refreshShims()
	err = d.Rescan()
	// Its Vite server restarts with the other Node.
	if name := processService("vite", s.Name); d.sup.PID(name) != 0 {
		d.stopProcess(s, "vite")
		d.setProblem(name, d.startProcess(s, "vite"))
	}
	d.changed()
	return err
}

// restartNodeProcesses restarts the running Vite servers, after a Node change.
func (d *Daemon) restartNodeProcesses() {
	d.mu.Lock()
	list := append([]sites.Site(nil), d.sites...)
	d.mu.Unlock()
	for _, s := range list {
		name := processService("vite", s.Name)
		if d.sup.PID(name) == 0 {
			continue
		}
		d.stopProcess(s, "vite")
		d.setProblem(name, d.startProcess(s, "vite"))
	}
}

func isNodeTool(name string) bool {
	for _, t := range install.NodeTools {
		if t == name {
			return true
		}
	}
	return false
}

// SetNodeTool installs (or updates to its latest version) or removes pnpm or yarn, as a global npm
// package, in the background; the panel and the CLI follow it like an installation.
func (d *Daemon) SetNodeTool(name string, on bool) (Job, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !isNodeTool(name) {
		return Job{}, fmt.Errorf("Nido instala pnpm y yarn, no %q", name)
	}
	if _, err := d.env.GlobalNode(); err != nil {
		return Job{}, err
	}
	action := "instalar"
	if !on {
		action = "quitar"
	}
	d.jobs.mu.Lock()
	defer d.jobs.mu.Unlock()
	for _, j := range d.jobs.jobs {
		if !j.Finished && j.Kind == name {
			return *j, nil
		}
	}
	id := make([]byte, 6)
	rand.Read(id)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	j := &Job{ID: hex.EncodeToString(id), Kind: name, Series: action, Phase: "preparando", Started: time.Now(), cancel: cancel}
	d.jobs.jobs[j.ID] = j
	go d.runNodeTool(ctx, j, name, on)
	return *j, nil
}

func (d *Daemon) runNodeTool(ctx context.Context, j *Job, name string, on bool) {
	defer j.cancel()
	logPath := d.env.Home.Logs("npm-global.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		args := []string{"uninstall", "--global", name}
		phase := "quitando " + name + " con npm"
		if on {
			args, phase = []string{"install", "--global", name + "@latest"}, "instalando "+name+" con npm"
		}
		d.jobs.update(j, func(j *Job) { j.Phase = phase })
		fmt.Fprintf(log, "\r\n==== %s · npm %s ====\r\n", time.Now().Format("2006-01-02 15:04:05"), strings.Join(args, " "))
		err = d.env.RunNPM(ctx, d.env.NPMPrefix(), log, args...)
		log.Close()
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		data, _ := os.ReadFile(logPath)
		if tail := procutil.LastLines(string(data), 4); tail != "" {
			err = fmt.Errorf("%w\n%s\n(registro completo: %s)", err, tail, logPath)
		}
	}
	d.refreshShims()
	version, _, _ := install.NodePackage(d.env.NPMPrefix(), name)
	d.jobs.update(j, func(j *Job) {
		j.Finished, j.Ended, j.Version = true, time.Now(), version
		switch {
		case errors.Is(err, context.Canceled):
			j.Phase, j.Error = "cancelado", "cancelado"
		case err != nil:
			j.Phase, j.Error = "error", err.Error()
		default:
			j.Phase = "listo"
		}
	})
	d.changed()
}

// ---- The package manager of a project ----

// jsRunner runs a project's package manager: npm's own script with the global Node, pnpm or yarn
// from Nido's global npm packages, or Yarn 2 and later through Corepack.
type jsRunner struct {
	name string   // npm, pnpm or yarn
	exe  string   // node.exe, or pnpm.exe
	args []string // what goes before the manager's own arguments
	env  []string // variables it needs (Corepack's)
}

func (r jsRunner) run(ctx context.Context, dir string, env []string, log *os.File, args ...string) error {
	return procutil.RunTree(ctx, dir, append(append([]string(nil), env...), r.env...), log, r.exe, append(append([]string(nil), r.args...), args...)...)
}

// toolMissing is the error of a project whose package manager is not installed yet.
type toolMissing string

func (t toolMissing) Error() string {
	if t == "corepack" {
		return "este proyecto usa Yarn 2 o posterior, que funciona con Corepack, y este Node no lo trae"
	}
	return fmt.Sprintf("este proyecto usa %s, que no está instalado: instálalo en «PHP y Node» o con 'nido node install %s'", string(t), string(t))
}

var berryRe = regexp.MustCompile(`^yarn@(\d+)\.`)

// yarnBerry reports whether a project asks for Yarn 2 or later in package.json ("yarn@4.9.2"): Yarn
// 1 refuses to work on it, Corepack fetches and runs the version it names.
func yarnBerry(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	m := berryRe.FindStringSubmatch(pkg.PackageManager)
	return m != nil && m[1] != "1"
}

// corepackDir is where Nido installs Corepack for the Node versions that no longer bring it.
func (d *Daemon) corepackDir() string { return d.env.Home.Data("corepack") }

// corepackScript finds Corepack: inside the global Node (it stopped coming with Node 25), in Nido's
// own copy, or among the global npm packages.
func (d *Daemon) corepackScript(node versions.Version) string {
	for _, p := range []string{
		filepath.Join(node.Path, "node_modules", "corepack", "dist", "corepack.js"),
		filepath.Join(d.corepackDir(), "node_modules", "corepack", "dist", "corepack.js"),
		filepath.Join(d.env.NPMPrefix(), "node_modules", "corepack", "dist", "corepack.js"),
	} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// projectManager is the package manager Nido runs for a project; Bun is not available in Nido yet,
// so its projects use npm.
func projectManager(dir string) string {
	if pm := sites.PackageManager(dir); pm != "bun" {
		return pm
	}
	return "npm"
}

// jsRunner is the package manager pm for the project in dir, with the Node its site uses.
func (d *Daemon) jsRunner(pm, dir string) (jsRunner, error) {
	node, err := d.env.NodeFor(dir)
	if err != nil {
		return jsRunner{}, err
	}
	r := jsRunner{name: pm, exe: filepath.Join(node.Path, "node.exe")}
	switch {
	case !isNodeTool(pm):
		r.name, r.args = "npm", []string{filepath.Join(node.Path, "node_modules", "npm", "bin", "npm-cli.js")}
		return r, nil
	case pm == "yarn" && yarnBerry(dir):
		script := d.corepackScript(node)
		if script == "" {
			return r, toolMissing("corepack")
		}
		// Corepack downloads the Yarn the project names without asking first.
		r.args, r.env = []string{script, "yarn"}, []string{"COREPACK_ENABLE_DOWNLOAD_PROMPT=0"}
		return r, nil
	}
	if _, bins, ok := install.NodePackage(d.env.NPMPrefix(), pm); ok && fileExists(bins[pm]) {
		r.exe, r.args = packageCommand(r.exe, bins[pm])
		return r, nil
	}
	return r, toolMissing(pm)
}

// ensureJSRunner is jsRunner, installing pnpm, yarn or Corepack first when the project needs it.
func (d *Daemon) ensureJSRunner(ctx context.Context, pm, dir string, phase func(string), log *os.File) (jsRunner, error) {
	r, err := d.jsRunner(pm, dir)
	var missing toolMissing
	if !errors.As(err, &missing) {
		return r, err
	}
	phase("instalando " + string(missing) + " con npm")
	if missing == "corepack" {
		// In a folder of its own: as a global package its pnpm and yarn commands would clash with
		// Nido's, and Nido only runs its script.
		own := d.corepackDir()
		if err := os.MkdirAll(own, 0o755); err != nil {
			return r, err
		}
		if err := d.env.RunNPM(ctx, own, log, "install", "corepack@latest", "--prefix", own, "--no-audit", "--no-fund"); err != nil {
			return r, fmt.Errorf("npm install corepack: %w", err)
		}
		return d.jsRunner(pm, dir)
	}
	if err := d.env.RunNPM(ctx, d.env.NPMPrefix(), log, "install", "--global", string(missing)+"@latest"); err != nil {
		return r, fmt.Errorf("npm install --global %s: %w", string(missing), err)
	}
	d.refreshShims()
	return d.jsRunner(pm, dir)
}

// installForVite installs the package manager a project's Vite server needs, when it is turned on
// before the project was prepared with it; npm's output goes to logs\npm-global.log.
func (d *Daemon) installForVite(pm, dir string) (jsRunner, error) {
	log, err := os.OpenFile(d.env.Home.Logs("npm-global.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return jsRunner{}, err
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return d.ensureJSRunner(ctx, pm, dir, func(string) {}, log)
}

// installArgs installs a project's dependencies; ignoreScripts leaves out the packages' install
// scripts, as Laravel's installer does for new projects.
func (r jsRunner) installArgs(ignoreScripts bool) []string {
	args := []string{"install"}
	if ignoreScripts {
		args = append(args, "--ignore-scripts")
	}
	if r.name == "npm" {
		args = append(args, "--no-audit", "--no-fund")
	}
	return args
}

// composerScripts are the Composer scripts of Laravel's kits that call npm, which Laravel's
// installer points at the package manager chosen for a new project.
var composerScripts = []string{"dev", "dev:ssr", "setup", "ci:check"}

// useComposerScripts rewrites npm and npx in those scripts for another package manager, like
// Laravel's installer (npm → pnpm, npx → pnpm dlx), leaving the rest of composer.json untouched.
func useComposerScripts(path, pm string) error {
	if pm == "npm" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	loc := regexp.MustCompile(`"scripts"\s*:\s*\{`).FindStringIndex(text)
	if loc == nil {
		return nil
	}
	end := jsonEnd(text, loc[1]-1)
	if end < 0 {
		return nil
	}
	scripts := text[loc[1]-1 : end]
	dlx := map[string]string{"pnpm": "pnpm dlx", "yarn": "npx"}[pm]
	for _, key := range composerScripts {
		k := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*:\s*`).FindStringIndex(scripts)
		if k == nil || k[1] >= len(scripts) {
			continue
		}
		vEnd := jsonEnd(scripts, k[1])
		if vEnd < 0 {
			continue
		}
		// The same replacements, in the same order: "pnpm" itself becomes "ppnpm" and is put back.
		value := strings.ReplaceAll(scripts[k[1]:vEnd], "npm", pm)
		value = strings.ReplaceAll(strings.ReplaceAll(value, "npx", dlx), "ppnpm", "pnpm")
		scripts = scripts[:k[1]] + value + scripts[vEnd:]
	}
	return os.WriteFile(path, []byte(text[:loc[1]-1]+scripts+text[end:]), 0o644)
}

// jsonEnd returns the index just past the JSON value that starts at text[start] (an array, an
// object or a string), or -1.
func jsonEnd(text string, start int) int {
	if start >= len(text) {
		return -1
	}
	if text[start] == '"' {
		for i := start + 1; i < len(text); i++ {
			switch text[i] {
			case '\\':
				i++
			case '"':
				return i + 1
			}
		}
		return -1
	}
	depth, inString := 0, false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case inString && c == '\\':
			i++
		case c == '"':
			inString = !inString
		case inString:
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
