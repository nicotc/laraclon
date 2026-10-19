package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/versions"
)

// PHPVersion resolves an installed PHP version ("8.3", "8.3.12"); "" is the global one.
func (e *Env) PHPVersion(want string) (versions.Version, error) {
	if strings.TrimSpace(want) == "" {
		return e.GlobalPHP()
	}
	v, ok := e.Reg.Find(versions.PHP, want)
	if !ok {
		return v, fmt.Errorf("PHP %s no está instalado", want)
	}
	return v, nil
}

// PHPExtensions lists what a PHP version can load (the DLLs it ships and the ones Nido downloaded)
// plus the extensions Nido can download for it, marked "descargable".
func (e *Env) PHPExtensions(v versions.Version) []php.Extension {
	list := php.Extensions(e.Home, v, e.Cfg.PHP.Extensions[versions.MajorMinor(v.Version)])
	have := map[string]bool{}
	for _, x := range list {
		have[x.Name] = true
	}
	for _, name := range install.DownloadableExtensions() {
		if !have[name] {
			list = append(list, php.Extension{Name: name, Source: "descargable", Zend: php.IsZend(name)})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

// SetPHPExtension switches an extension on or off for a PHP series ("8.4"), downloading it first
// when Nido can. An extension switched on must load in that PHP; when it does not, nothing changes
// and the error says why (a missing DLL it depends on, for example).
func (e *Env) SetPHPExtension(ctx context.Context, v versions.Version, name string, on bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimSuffix(strings.TrimPrefix(name, "php_"), ".dll")
	series := versions.MajorMinor(v.Version)
	find := func() (php.Extension, bool) {
		for _, x := range php.Extensions(e.Home, v, e.Cfg.PHP.Extensions[series]) {
			if x.Name == name {
				return x, true
			}
		}
		return php.Extension{}, false
	}
	ext, ok := find()
	if !ok && !on {
		return nil
	}
	if !ok {
		if err := e.DownloadExtension(ctx, v, name); err != nil {
			return err
		}
		if ext, ok = find(); !ok {
			return fmt.Errorf("PHP %s no encuentra la extensión %s", v.Version, name)
		}
	}
	prev := e.Cfg.PHP.Extensions[series]
	restore := func() {
		if e.Cfg.PHP.Extensions == nil {
			e.Cfg.PHP.Extensions = map[string]config.PHPExtensions{}
		}
		if len(prev.Enable) == 0 && len(prev.Disable) == 0 {
			delete(e.Cfg.PHP.Extensions, series)
		} else {
			e.Cfg.PHP.Extensions[series] = prev
		}
	}
	e.Cfg.PHP.SetExtension(series, name, on, ext.Default)
	ini, err := php.WriteIni(e.Home, v, e.IniOptions())
	if err == nil && on {
		err = checkExtension(v, ini, name)
	}
	if err != nil {
		restore()
		php.WriteIni(e.Home, v, e.IniOptions())
		return err
	}
	return e.SaveConfig()
}

// DownloadExtension fetches the build of an extension for a PHP version into Nido's folder, without
// switching it on.
func (e *Env) DownloadExtension(ctx context.Context, v versions.Version, name string) error {
	ts := v.ThreadSafe != nil && *v.ThreadSafe
	rel, err := install.FindPHPExtension(ctx, e.Home, name, versions.MajorMinor(v.Version), ts)
	if err != nil {
		return err
	}
	if _, err := install.InstallPHPExtension(ctx, e.Home, rel, php.ExtDir(e.Home, v)); err != nil {
		return fmt.Errorf("%s %s: %w", name, rel.Version, err)
	}
	return nil
}

// loadedNames maps extensions whose name inside PHP differs from their DLL.
var loadedNames = map[string]string{"opcache": "Zend OPcache"}

// checkExtension asks the PHP version, with its new php.ini, whether the extension loads.
func checkExtension(v versions.Version, ini, name string) error {
	loaded := name
	if n, ok := loadedNames[name]; ok {
		loaded = n
	}
	out, err := procutil.Output(30*time.Second, v.Path, filepath.Join(v.Path, "php.exe"), "-c", ini, "-r", fmt.Sprintf("echo extension_loaded('%s') ? 'nido-ok' : 'nido-no';", loaded))
	if err == nil && strings.HasSuffix(strings.TrimSpace(out), "nido-ok") {
		return nil
	}
	detail := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(out), "nido-no"))
	if detail == "" && err != nil {
		detail = err.Error()
	}
	if detail == "" {
		detail = "PHP no la cargó"
	}
	return fmt.Errorf("la extensión %s no carga en PHP %s: %s", name, v.Version, procutil.LastLines(detail, 3))
}

// PHPExtensions lists the extensions of a PHP version ("" = the global one).
func (d *Daemon) PHPExtensions(version string) (versions.Version, []php.Extension, error) {
	v, err := d.env.PHPVersion(version)
	if err != nil {
		return v, nil, err
	}
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	return v, d.env.PHPExtensions(v), nil
}

// SetPHPExtension switches an extension of a PHP version ("" = the global one) and restarts the
// php-cgi processes that run that version, so the sites use it at once.
func (d *Daemon) SetPHPExtension(version, name string, on bool) error {
	v, err := d.env.PHPVersion(version)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	d.cfgMu.Lock()
	err = d.env.SetPHPExtension(ctx, v, name, on)
	d.cfgMu.Unlock()
	if err != nil {
		return err
	}
	d.refreshShims()
	err = d.restartPHPVersion(v)
	d.changed()
	return err
}

// restartPHPVersion restarts the running php-cgi processes of a version (the global pool, a pinned
// one, or both) so they read their new php.ini.
func (d *Daemon) restartPHPVersion(v versions.Version) error {
	global, _ := d.env.GlobalPHP()
	var errs []string
	for _, p := range d.currentPools() {
		version := p.Version.Version
		if p.Global {
			version = global.Version
		}
		if version != v.Version {
			continue
		}
		names := map[string]bool{}
		running := false
		for _, port := range p.Ports {
			names[php.PoolName(port)] = true
			running = running || d.sup.PID(php.PoolName(port)) != 0
		}
		if !running {
			continue
		}
		d.sup.StopMatching(func(n string) bool { return names[n] })
		var err error
		if p.Global {
			err = d.startPHP()
			d.setProblem("php", err)
			d.setProblem("salud", d.startSalud(true)) // it shows the global PHP's extensions
		} else {
			err = d.startPinnedPools([]phpPool{p})
		}
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
