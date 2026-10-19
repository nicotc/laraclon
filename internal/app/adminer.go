package app

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/proxy"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// Adminer runs with the global PHP's built-in web server on an internal port, behind the proxy at
// https://db.localhost (browsers resolve *.localhost to this computer on their own).
const (
	adminerService = "adminer"
	adminerPort    = 19900
	adminerHost    = "db.localhost"
)

// adminerIndex lets Adminer open the local databases without a password (MySQL's root and
// PostgreSQL's postgres have none here), which Adminer refuses by default. Only this computer
// reaches it: the PHP server listens on 127.0.0.1 and the proxy refuses other addresses.
const adminerIndex = `<?php
// Generado por clon: Adminer para las bases de datos locales, sin contraseña y solo desde este equipo.
function adminer_object() {
    return new class extends \Adminer\Adminer {
        function login($login, $password) { return true; }
    };
}
require %s;
`

// phpString is a single-quoted PHP string literal (no escapes are interpreted there but \\ and \').
func phpString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func (d *Daemon) adminerVersion() (versions.Version, bool) {
	return d.env.Reg.Find(versions.Adminer, "")
}

// AdminerURL is Adminer's address through the proxy ("" when it is not installed).
func (d *Daemon) AdminerURL() string {
	if _, ok := d.adminerVersion(); !ok {
		return ""
	}
	u := "https://" + adminerHost
	if p := d.env.Cfg.Proxy.HTTPSPort; p != 443 {
		u += ":" + strconv.Itoa(p)
	}
	return u + "/"
}

// adminerRoute is db.localhost in the proxy (none when Adminer is not installed).
func (d *Daemon) adminerRoute() (proxy.Route, bool) {
	v, ok := d.adminerVersion()
	if !ok {
		return proxy.Route{}, false
	}
	backend, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", adminerPort))
	return proxy.Route{Site: adminerHost, Server: "Adminer " + v.Version, Backend: backend, HTTPS: true, LocalOnly: true}, true
}

// startAdminer serves the installed Adminer (while clon's services are on); a running one restarts
// when restart is set, to pick up another PHP or Adminer version.
func (d *Daemon) startAdminer(restart bool) error {
	v, ok := d.adminerVersion()
	d.mu.Lock()
	on := d.servicesOn
	d.mu.Unlock()
	if !ok || !on {
		return nil
	}
	if d.sup.PID(adminerService) != 0 {
		if !restart {
			return nil
		}
		d.sup.Stop(adminerService)
	}
	global, err := d.env.GlobalPHP()
	if err != nil {
		return fmt.Errorf("Adminer necesita el PHP global: %w", err)
	}
	ini, err := php.WriteIni(d.env.Home, global, d.env.IniOptions())
	if err != nil {
		return err
	}
	root := d.env.Home.Generated("adminer")
	index := fmt.Sprintf(adminerIndex, phpString(paths.Slash(filepath.Join(v.Path, "adminer.php"))))
	if err := config.WriteFileAtomic(filepath.Join(root, "index.php"), []byte(index)); err != nil {
		return err
	}
	if err := ports.Check(adminerPort); err != nil {
		return fmt.Errorf("Adminer no puede usar su puerto interno: %w", err)
	}
	return d.sup.Start(supervisor.Spec{
		Name:    adminerService,
		Exe:     filepath.Join(global.Path, "php.exe"),
		Args:    []string{"-S", fmt.Sprintf("127.0.0.1:%d", adminerPort), "-t", root, "-c", ini},
		Dir:     root,
		Env:     []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH()},
		LogPath: d.env.Home.Logs("adminer.log"),
		Port:    adminerPort,
		Restart: true,
	})
}
