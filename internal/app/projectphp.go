package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/phpreq"
	"github.com/nicotc/laraclon/internal/versions"
)

// lockedPHP makes sure a project's Composer packages accept the PHP it will run with. When they do
// not (a composer.lock pinned to packages that stop before PHP 8.5, say), it picks the newest PHP that
// satisfies the project and every locked package: an installed one first, otherwise the newest branch
// windows.php.net has, which it downloads. The site is then pinned to it. It returns the PHP to use.
func (d *Daemon) lockedPHP(ctx context.Context, dir, site string, cur versions.Version, phase func(string), log io.Writer) (versions.Version, error) {
	reqs, pinned, err := phpreq.Read(dir)
	if err != nil || pinned || len(reqs) == 0 {
		return cur, nil // no composer.json, or Composer told to pretend a version: nothing to check
	}
	unmet := phpreq.Unmet(cur.Version, reqs)
	if unmet == nil {
		return cur, nil
	}
	fmt.Fprintf(log, "\r\nPHP %s no sirve: %s pide php %s\r\n", cur.Version, unmet.From, unmet.Constraint)
	pick, ok := versions.Version{}, false
	for _, v := range d.env.Reg.List(versions.PHP) { // newest first
		if phpreq.Unmet(v.Version, reqs) == nil {
			pick, ok = v, true
			break
		}
	}
	if !ok {
		list, err := install.PHPCatalog(ctx)
		if err != nil {
			return cur, fmt.Errorf("%s pide php %s y PHP %s no sirve; no se pudo consultar windows.php.net: %w", unmet.From, unmet.Constraint, cur.Version, err)
		}
		for _, rel := range list {
			if phpreq.Unmet(rel.Version, reqs) != nil {
				continue
			}
			phase(fmt.Sprintf("descargando PHP %s, el que pide el proyecto (%s: php %s)", rel.Branch, unmet.From, unmet.Constraint))
			v, err := install.InstallPHP(ctx, d.env.Home, rel.Branch, nil)
			if err != nil {
				return cur, err
			}
			if err := d.registerPHP(v, false); err != nil {
				return cur, err
			}
			pick, ok = v, true
			break
		}
	}
	if !ok {
		return cur, fmt.Errorf("ningún PHP para Windows satisface lo que pide el proyecto (%s: php %s)", unmet.From, unmet.Constraint)
	}
	branch := versions.MajorMinor(pick.Version)
	phase(fmt.Sprintf("usando PHP %s en este sitio: %s pide php %s", branch, unmet.From, unmet.Constraint))
	if err := d.SetSitePHP(site, branch); err != nil {
		// The site may not be published yet (a project being cloned): the setting alone is enough.
		if !strings.Contains(err.Error(), "no hay ningún sitio") {
			return cur, err
		}
		d.cfgMu.Lock()
		d.env.Cfg.SetSitePHP(site, branch)
		err = d.env.SaveConfig()
		d.cfgMu.Unlock()
		if err != nil {
			return cur, err
		}
	}
	return pick, nil
}
