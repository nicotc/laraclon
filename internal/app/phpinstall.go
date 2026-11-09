package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/versions"
)

// MaxUpload bounds a .zip sent to the panel: the largest official builds (MySQL, PostgreSQL) are
// under 500 MB.
const MaxUpload = 1 << 30

// PHPCatalog lists the PHP branches that can be installed, maintained and archived.
func (d *Daemon) PHPCatalog(ctx context.Context) ([]install.PHPRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	list, err := install.PHPCatalog(ctx)
	return orEmpty(list), err
}

// InstallPHPBranch downloads the newest release of a PHP branch ("8.3", "7.4") in the background;
// use makes it the global PHP once installed.
func (d *Daemon) InstallPHPBranch(branch string, use bool) (Job, error) {
	branch = versions.MajorMinor(strings.TrimSpace(branch))
	if branch == "" {
		return Job{}, fmt.Errorf("falta la rama de PHP, por ejemplo 8.4")
	}
	list, err := d.PHPCatalog(context.Background())
	if err != nil {
		return Job{}, err
	}
	var rel *install.PHPRelease
	for i := range list {
		if list[i].Branch == branch {
			rel = &list[i]
		}
	}
	if rel == nil {
		return Job{}, fmt.Errorf("windows.php.net no tiene PHP %s para Windows de 64 bits; sube su .zip si lo tienes", branch)
	}
	if v, ok := d.env.Reg.Find(versions.PHP, rel.Version); ok && v.Version == rel.Version {
		return Job{}, fmt.Errorf("%s ya está instalado", v.Label())
	}
	j, snap, ctx, running := d.newJob(string(versions.PHP), branch, rel.Version, 0)
	if !running {
		go func() {
			defer j.cancel()
			d.jobs.update(j, func(j *Job) { j.Phase = "descargando" })
			v, err := install.InstallPHP(ctx, d.env.Home, branch, d.jobProgress(j))
			if err == nil {
				d.jobs.update(j, func(j *Job) { j.Phase = "poniéndolo en marcha" })
				err = d.registerPHP(v, use)
			}
			d.endJob(j, err)
		}()
	}
	return snap, nil
}

// registerPHP adds a PHP that is now in bin\ and puts it to use: the first one becomes the global PHP
// (and starts if Nido's services are on), use makes any other the global one, and sites that pinned
// its branch while it was missing move to it.
func (d *Daemon) registerPHP(v versions.Version, use bool) error {
	d.cfgMu.Lock()
	d.env.Reg.Add(v)
	err := d.env.SaveRegistry()
	first := false
	if _, gerr := d.env.GlobalPHP(); gerr != nil {
		first = true
		d.env.Cfg.PHP.Version = v.Version
		if err == nil {
			err = d.env.SaveConfig()
		}
	}
	d.cfgMu.Unlock()
	if err != nil {
		return err
	}
	d.refreshShims()
	if use && !first {
		_, err = d.UsePHP(v.Version)
		return err
	}
	d.mu.Lock()
	on := d.servicesOn
	d.mu.Unlock()
	if first && on {
		d.setProblem("php", d.startPHP())
	}
	if err := d.Rescan(); err != nil {
		d.setProblem("sitios", err)
	}
	d.changed()
	return nil
}

// UninstallPHP removes a PHP version that neither the global PHP nor any site uses. A version Nido
// put in bin\ is deleted; one found elsewhere is only forgotten.
func (d *Daemon) UninstallPHP(version string) error {
	v, ok := d.env.Reg.Find(versions.PHP, version)
	if !ok || v.Version != strings.TrimSpace(version) {
		return fmt.Errorf("PHP %s no está instalado", version)
	}
	if g, err := d.env.GlobalPHP(); err == nil && g.Version == v.Version {
		return fmt.Errorf("PHP %s es el PHP global: elige otro como global antes de desinstalarlo", v.Version)
	}
	var users []string
	d.mu.Lock()
	for _, s := range d.sites {
		if s.PHPVersion == v.Version {
			users = append(users, s.Host)
		}
	}
	d.mu.Unlock()
	if len(users) > 0 {
		return fmt.Errorf("PHP %s lo usan %s: cambia su PHP en la ficha del sitio antes de desinstalarlo", v.Version, strings.Join(users, ", "))
	}
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	if install.OwnedByNido(d.env.Home, v) {
		if err := os.RemoveAll(v.Path); err != nil {
			return fmt.Errorf("no se pudo borrar %s (¿algún programa lo está usando?): %w", v.Path, err)
		}
	}
	d.env.Reg.Remove(versions.PHP, v.Version)
	if err := d.env.SaveRegistry(); err != nil {
		return err
	}
	d.refreshShims()
	d.changed()
	return nil
}

// ImportUpload installs a .zip sent from the panel (or 'nido import') as kind: it is saved to cache\,
// checked and unpacked like a download, and put to use like one.
func (d *Daemon) ImportUpload(kind versions.Component, body io.Reader) (versions.Version, error) {
	if !install.CanImport(kind) {
		return versions.Version{}, fmt.Errorf("%s no se puede instalar desde un .zip", kind.Name())
	}
	dir := d.env.Home.Cache("uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return versions.Version{}, err
	}
	f, err := os.CreateTemp(dir, string(kind)+"-*.zip")
	if err != nil {
		return versions.Version{}, err
	}
	zipPath := f.Name()
	defer os.Remove(zipPath)
	n, err := io.Copy(f, io.LimitReader(body, MaxUpload+1))
	f.Close()
	switch {
	case err != nil:
		return versions.Version{}, fmt.Errorf("no se pudo recibir el .zip: %w", err)
	case n == 0:
		return versions.Version{}, fmt.Errorf("el .zip llegó vacío")
	case n > MaxUpload:
		return versions.Version{}, fmt.Errorf("el .zip pasa de %d MB", MaxUpload>>20)
	}
	taken := func(version string) bool {
		v, ok := d.env.Reg.Find(kind, version)
		return ok && v.Version == version
	}
	v, err := install.ImportZip(d.env.Home, kind, zipPath, taken)
	if err != nil {
		return v, err
	}
	if kind == versions.PHP {
		return v, d.registerPHP(v, false)
	}
	rel := install.Release{Kind: kind, Series: versions.Series(kind, v.Version), Version: v.Version, Source: install.SourceZip}
	return v, d.finishInstall(rel, v, false, nil)
}
