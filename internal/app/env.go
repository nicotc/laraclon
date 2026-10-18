// Package app ties configuration, installed versions and services together. It is shared by the
// command line (clon.exe) and the background process with the tray icon (clonw.exe).
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/shims"
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
	return &Env{Home: home, Cfg: cfg, Reg: reg}, nil
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

// MySQLVersion is the MySQL used by the mysql service and the mysql shim.
func (e *Env) MySQLVersion() (versions.Version, error) {
	v, ok := e.Reg.Find(versions.MySQL, e.Cfg.MySQL.Version)
	if !ok {
		return v, fmt.Errorf("no hay ninguna versión de MySQL instalada")
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
	return php.IniOptions{CABundle: e.CABundle(), SMTPPort: 1025}
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
		targets["php"] = shims.Target{Exe: phpExe, Args: []string{"-c", ini}, ForceEnv: phpEnv(ini)}
		if phar := install.ComposerPhar(e.Home); fileExists(phar) {
			targets["composer"] = shims.Target{Exe: phpExe, Args: []string{"-c", ini, phar}, ForceEnv: phpEnv(ini)}
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
	}

	if v, err := e.MySQLVersion(); err == nil {
		if client := filepath.Join(v.Path, "bin", "mysql.exe"); fileExists(client) {
			targets["mysql"] = shims.Target{Exe: client, Args: []string{"--host=127.0.0.1", fmt.Sprintf("--port=%d", e.Cfg.MySQL.Port), "--user=root"}}
		}
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
