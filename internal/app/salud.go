package app

import (
	_ "embed"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/proxy"
	"github.com/nicotc/laraclon/internal/supervisor"
)

// The health page runs with the global PHP's built-in web server on an internal port, behind the
// proxy at https://salud.localhost: it asks Nido's API which services and sites there are, and tries
// each connection with PHP itself, the way a project would.
const (
	saludService = "salud"
	saludPort    = 19901
	saludHost    = "salud.localhost"
)

//go:embed salud/index.php
var saludPage []byte

// saludConfig tells the page where Nido's API is (port and key in run\control.json).
const saludConfig = `<?php
// Generado por Nido: dónde está su API (el puerto y la clave están en run\control.json).
return ['control' => %s, 'nido' => %s];
`

// HealthURL is the health page's address through the proxy.
func (d *Daemon) HealthURL() string {
	u := "https://" + saludHost
	if p := d.env.Cfg.Proxy.HTTPSPort; p != 443 {
		u += ":" + strconv.Itoa(p)
	}
	return u + "/"
}

// saludRoute is salud.localhost in the proxy (none until there is a PHP to run it).
func (d *Daemon) saludRoute() (proxy.Route, bool) {
	global, err := d.env.GlobalPHP()
	if err != nil {
		return proxy.Route{}, false
	}
	backend, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", saludPort))
	return proxy.Route{Site: saludHost, Server: "PHP " + global.Version, Backend: backend, HTTPS: true, LocalOnly: true}, true
}

// startSalud serves the health page while Nido's services are on; a running one restarts when
// restart is set, to follow another global PHP or its extensions.
func (d *Daemon) startSalud(restart bool) error {
	d.mu.Lock()
	on := d.servicesOn
	d.mu.Unlock()
	if !on {
		return nil
	}
	if d.sup.PID(saludService) != 0 {
		if !restart {
			return nil
		}
		d.sup.Stop(saludService)
	}
	global, err := d.env.GlobalPHP()
	if err != nil {
		return nil // no PHP installed yet: the page has nothing to run on
	}
	ini, err := php.WriteIni(d.env.Home, global, d.env.IniOptions())
	if err != nil {
		return err
	}
	root := d.env.Home.Generated("salud")
	if err := config.WriteFileAtomic(filepath.Join(root, "index.php"), saludPage); err != nil {
		return err
	}
	control := paths.Slash(filepath.Join(d.env.Home.Root, "run", "control.json"))
	cfg := fmt.Sprintf(saludConfig, phpString(control), phpString(Version))
	if err := config.WriteFileAtomic(filepath.Join(root, "config.php"), []byte(cfg)); err != nil {
		return err
	}
	if err := ports.Check(saludPort); err != nil {
		return fmt.Errorf("la página de salud no puede usar su puerto interno: %w", err)
	}
	return d.sup.Start(supervisor.Spec{
		Name:    saludService,
		Exe:     filepath.Join(global.Path, "php.exe"),
		Args:    []string{"-S", fmt.Sprintf("127.0.0.1:%d", saludPort), "-t", root, "-c", ini},
		Dir:     root,
		Env:     []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH()},
		LogPath: d.env.Home.Logs("salud.log"),
		Port:    saludPort,
		Restart: true,
	})
}
