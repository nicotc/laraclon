// Package app ties configuration, installed versions and services together. It is shared by the
// command line (clon.exe) and the background process with the tray icon (clonw.exe).
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/mongodb"
	"github.com/nicotc/laraclon/internal/mysql"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/postgres"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/redis"
	"github.com/nicotc/laraclon/internal/shims"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

// Env is the loaded state of one installation.
type Env struct {
	Home paths.Home
	Cfg  config.Config
	Reg  *versions.Registry
}

// Open resolves the installation folder, creates its layout and loads settings and versions.
func Open() (*Env, error) {
	home, err := paths.Resolve()
	if err != nil {
		return nil, err
	}
	if err := home.Ensure(); err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigFile())
	if err != nil {
		return nil, fmt.Errorf("error en %s: %w", home.ConfigFile(), err)
	}
	reg, err := versions.Load(home.State("versions.json"))
	if err != nil {
		return nil, err
	}
	e := &Env{Home: home, Cfg: cfg, Reg: reg}
	if e.SyncServices() {
		if err := e.SaveConfig(); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func (e *Env) SaveConfig() error   { return config.Save(e.Home.ConfigFile(), e.Cfg) }
func (e *Env) SaveRegistry() error { return e.Reg.Save() }

// GlobalPHP is the PHP version used by the web pool and by `php` in terminals.
func (e *Env) GlobalPHP() (versions.Version, error) {
	v, ok := e.Reg.Find(versions.PHP, e.Cfg.PHP.Version)
	if !ok {
		if e.Cfg.PHP.Version == "" {
			return v, fmt.Errorf("no hay ninguna versión de PHP instalada (usa 'clon php install 8.4' o 'clon import laragon')")
		}
		return v, fmt.Errorf("PHP %s no está instalado", e.Cfg.PHP.Version)
	}
	return v, nil
}

// GlobalNode is the Node version behind node, npm and npx.
func (e *Env) GlobalNode() (versions.Version, error) {
	v, ok := e.Reg.Find(versions.Node, e.Cfg.Node.Version)
	if !ok {
		return v, fmt.Errorf("no hay ninguna versión de Node instalada (usa 'clon node install lts')")
	}
	return v, nil
}

// CABundle is the PEM file PHP uses for HTTPS: the public roots plus the local CA ("" if absent).
func (e *Env) CABundle() string {
	p := e.Home.SSL("cacert-bundle.pem")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if install.HaveCABundle(e.Home) {
		return install.MozillaBundle(e.Home)
	}
	return ""
}

// IniOptions are the managed php.ini values for this installation.
func (e *Env) IniOptions() php.IniOptions {
	return php.IniOptions{CABundle: e.CABundle(), SMTPPort: 1025, Extensions: e.Cfg.PHP.Extensions}
}

// EnsureComposerFor downloads Composer 2.2 LTS when a pinned PHP is too old for the current
// Composer and it is not there yet.
func (e *Env) EnsureComposerFor(ctx context.Context, phpVersion string) error {
	if !install.NeedsComposerLTS(phpVersion) || fileExists(install.ComposerLTSPhar(e.Home)) {
		return nil
	}
	_, err := install.InstallComposerLTS(ctx, e.Home)
	return err
}

// ShimTemplate is clon-shim.exe, installed next to clon.exe.
func (e *Env) ShimTemplate() string { return e.Home.Path("clon-shim.exe") }

// NPMPrefix keeps npm global packages outside the Node folders, so they survive version changes.
func (e *Env) NPMPrefix() string { return e.Home.Data("npm-global") }

// ChildPATH is the PATH given to services: clon's shims first, so exec('php'), exec('node') or
// exec('composer') from PHP code run the active versions instead of whatever else is installed.
func (e *Env) ChildPATH() string {
	return "PATH=" + e.Home.Shims() + ";" + e.NPMPrefix() + ";" + os.Getenv("PATH")
}

// packageCommand is how a command of an npm package runs: a native program directly (pnpm ships
// pnpm.exe since version 11), a script with the global Node.
func packageCommand(nodeExe, file string) (exe string, args []string) {
	if strings.EqualFold(filepath.Ext(file), ".exe") {
		return file, nil
	}
	return nodeExe, []string{file}
}

// RunNPM runs the global Node's npm in dir, its global packages going to NPMPrefix.
func (e *Env) RunNPM(ctx context.Context, dir string, out io.Writer, args ...string) error {
	node, err := e.GlobalNode()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.NPMPrefix(), 0o755); err != nil {
		return err
	}
	cli := filepath.Join(node.Path, "node_modules", "npm", "bin", "npm-cli.js")
	env := []string{e.ChildPATH(), "NPM_CONFIG_PREFIX=" + e.NPMPrefix()}
	return procutil.RunTree(ctx, dir, env, out, filepath.Join(node.Path, "node.exe"), append([]string{cli}, args...)...)
}

// RefreshShims regenerates php.ini for every PHP version and rewrites the launchers in shims\.
func (e *Env) RefreshShims() (map[string]shims.Target, error) {
	targets := map[string]shims.Target{}

	inis := map[string]string{}
	iniFor := func(v versions.Version) (string, error) {
		if p, ok := inis[v.Version]; ok {
			return p, nil
		}
		p, err := php.WriteIni(e.Home, v, e.IniOptions())
		inis[v.Version] = p
		return p, err
	}

	newestPerBranch := map[string]versions.Version{}
	for _, v := range e.Reg.List(versions.PHP) {
		mm := versions.MajorMinor(v.Version)
		if _, ok := newestPerBranch[mm]; !ok {
			newestPerBranch[mm] = v
		}
	}
	// PHPRC makes child processes (Composer's @php scripts, Artisan, exec('php')) load clon's
	// php.ini instead of none, or whatever php.ini sits next to php.exe (Laragon's, for example).
	phpEnv := func(ini string) map[string]string { return map[string]string{"PHPRC": filepath.Dir(ini)} }
	for mm, v := range newestPerBranch {
		ini, err := iniFor(v)
		if err != nil {
			return nil, err
		}
		targets["php"+strings.ReplaceAll(mm, ".", "")] = shims.Target{Exe: filepath.Join(v.Path, "php.exe"), Args: []string{"-c", ini}, ForceEnv: phpEnv(ini)}
	}
	if v, err := e.GlobalPHP(); err == nil {
		ini, err := iniFor(v)
		if err != nil {
			return nil, err
		}
		phpExe := filepath.Join(v.Path, "php.exe")
		phar := install.ComposerPhar(e.Home)
		phpT := shims.Target{Exe: phpExe, Args: []string{"-c", ini}, ForceEnv: phpEnv(ini)}
		composerT := shims.Target{Exe: phpExe, Args: []string{"-c", ini, phar}, ForceEnv: phpEnv(ini)}
		// Inside a project that pinned its own PHP, php and composer (and so Artisan) use that one.
		list, _ := sites.Scan(e.Cfg)
		for _, s := range list {
			if s.PHP == "" || s.Server == config.ServerFrankenPHP {
				continue
			}
			pv, ok := e.Reg.Find(versions.PHP, s.PHP)
			if !ok || pv.Version == v.Version {
				continue
			}
			pini, err := iniFor(pv)
			if err != nil {
				return nil, err
			}
			pexe := filepath.Join(pv.Path, "php.exe")
			phpT.Dirs = append(phpT.Dirs, shims.DirTarget{Dir: s.Folder, Exe: pexe, Args: []string{"-c", pini}, ForceEnv: phpEnv(pini)})
			// PHP older than 7.2.5 needs Composer 2.2 LTS (fetched when the site is pinned).
			if pphar := install.ComposerFor(e.Home, pv.Version); fileExists(pphar) {
				composerT.Dirs = append(composerT.Dirs, shims.DirTarget{Dir: s.Folder, Exe: pexe, Args: []string{"-c", pini, pphar}, ForceEnv: phpEnv(pini)})
			}
		}
		targets["php"] = phpT
		if fileExists(phar) {
			targets["composer"] = composerT
		}
		// phpx: the global PHP with Xdebug waiting for the editor, for Artisan and tests.
		if php.HasXdebug(e.Home, v) {
			opts := e.IniOptions()
			opts.Debug = true
			if dini, err := php.WriteIni(e.Home, v, opts); err == nil {
				targets["phpx"] = shims.Target{Exe: phpExe, Args: []string{"-c", dini}, ForceEnv: phpEnv(dini)}
			}
		}
	}

	if v, err := e.GlobalNode(); err == nil {
		nodeExe := filepath.Join(v.Path, "node.exe")
		env := map[string]string{"NPM_CONFIG_PREFIX": e.NPMPrefix()}
		targets["node"] = shims.Target{Exe: nodeExe}
		if cli := filepath.Join(v.Path, "node_modules", "npm", "bin", "npm-cli.js"); fileExists(cli) {
			targets["npm"] = shims.Target{Exe: nodeExe, Args: []string{cli}, Env: env}
		}
		if cli := filepath.Join(v.Path, "node_modules", "npm", "bin", "npx-cli.js"); fileExists(cli) {
			targets["npx"] = shims.Target{Exe: nodeExe, Args: []string{cli}, Env: env}
		}
		if err := os.MkdirAll(e.NPMPrefix(), 0o755); err != nil {
			return nil, err
		}
		// pnpm and yarn (pnpx, yarnpkg…), from clon's global npm packages.
		for _, tool := range install.NodeTools {
			if _, bins, ok := install.NodePackage(e.NPMPrefix(), tool); ok {
				for cmd, file := range bins {
					if fileExists(file) {
						exe, args := packageCommand(nodeExe, file)
						targets[cmd] = shims.Target{Exe: exe, Args: args, Env: env}
					}
				}
			}
		}
	}

	// `mysql` opens the default database (MySQL, or MariaDB when there is no MySQL).
	if db, ok := e.DefaultDB(); ok {
		if client := mysql.ClientExe(db.Version); client != "" {
			targets["mysql"] = shims.Target{Exe: client, Args: []string{"--host=127.0.0.1", fmt.Sprintf("--port=%d", db.Instance.Port), "--user=root"}}
		}
	}
	// `psql` and `redis-cli` open the default PostgreSQL and Redis.
	if ref, ok := e.DefaultInstance(versions.PostgreSQL); ok {
		if psql := postgres.Psql(ref.Version); psql != "" {
			targets["psql"] = shims.Target{Exe: psql, Args: []string{"-h", "127.0.0.1", "-p", fmt.Sprint(ref.Instance.Port), "-U", "postgres"}}
		}
	}
	if ref, ok := e.DefaultInstance(versions.Redis); ok {
		if cli := redis.CLI(ref.Version); cli != "" {
			targets["redis-cli"] = shims.Target{Exe: cli, Args: []string{"-p", fmt.Sprint(ref.Instance.Port)}}
		}
	}
	// `mongosh` opens the default MongoDB (its standard port needs no option, which keeps URIs usable).
	if ref, ok := e.DefaultInstance(versions.MongoDB); ok {
		if shell := mongodb.Shell(ref.Version); shell != "" {
			t := shims.Target{Exe: shell}
			if ref.Instance.Port != StandardPort(versions.MongoDB) {
				t.Args = []string{"--port", fmt.Sprint(ref.Instance.Port)}
			}
			targets["mongosh"] = t
		}
	}

	// Every launcher puts clon's tools first in its child's PATH: npm scripts call `node`, Composer
	// scripts call `php`, and on a machine where the user's PATH lacks them they would fail.
	for name, t := range targets {
		t.PathPrepend = []string{e.Home.Shims(), e.NPMPrefix()}
		targets[name] = t
	}

	if !fileExists(e.ShimTemplate()) {
		return targets, fmt.Errorf("falta %s (compila cmd/clon-shim)", e.ShimTemplate())
	}
	return targets, shims.Write(e.Home, e.ShimTemplate(), targets)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
