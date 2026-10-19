package app

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/frankenphp"
	"github.com/nicotc/laraclon/internal/nginx"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/proxy"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// serverPorts says where the sites of an optional web server get their internal ports.
type serverPorts struct {
	store    *sites.PortStore
	from, to int
}

func (d *Daemon) serverPorts() map[string]serverPorts {
	return map[string]serverPorts{
		config.ServerNginx:      {d.ngx, nginxPortFrom, nginxPortTo},
		config.ServerFrankenPHP: {d.fphp, frankenPortFrom, frankenPortTo},
	}
}

// assignPorts gives every site its Apache port (kept even while it uses another server, so
// switching back finds the same one) and the sites of the other servers a port of their own.
// Site.Port ends up as the port of the server the site uses, where the proxy sends its requests.
// own lists, per server, the ports clon's servers hold right now (they are not conflicts).
func assignPorts(list []sites.Site, apacheStore *sites.PortStore, others map[string]serverPorts, own map[string]map[int]bool) error {
	if err := apacheStore.Assign(list, sitePortFrom, sitePortTo, own[config.ServerApache]); err != nil {
		return err
	}
	if err := apacheStore.Save(); err != nil {
		return err
	}
	for server, sp := range others {
		var subset []sites.Site
		for _, s := range list {
			if s.Server == server {
				s.Port = 0
				subset = append(subset, s)
			}
		}
		if len(subset) == 0 {
			continue
		}
		if err := sp.store.Assign(subset, sp.from, sp.to, own[server]); err != nil {
			return err
		}
		if err := sp.store.Save(); err != nil {
			return err
		}
		byName := map[string]int{}
		for _, s := range subset {
			byName[s.Name] = s.Port
		}
		for i := range list {
			if list[i].Server == server {
				list[i].Port = byName[list[i].Name]
			}
		}
	}
	return nil
}

// routes maps every host and alias to the internal port of the site's server.
func (d *Daemon) routes(list []sites.Site) map[string]proxy.Route {
	labels := map[string]string{config.ServerApache: "Apache", config.ServerNginx: "Nginx", config.ServerFrankenPHP: "FrankenPHP"}
	if v, err := d.apacheVersion(); err == nil {
		labels[config.ServerApache] = "Apache " + v.Version
	}
	if v, err := d.nginxVersion(); err == nil {
		labels[config.ServerNginx] = "Nginx " + v.Version
	}
	if v, err := d.frankenVersion(); err == nil {
		labels[config.ServerFrankenPHP] = "FrankenPHP " + v.Version
	}
	routes := map[string]proxy.Route{}
	for _, s := range list {
		backend, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.Port))
		r := proxy.Route{Site: s.Host, Server: labels[s.Server], Backend: backend, HTTPS: s.HTTPS}
		routes[s.Host] = r
		for _, a := range s.Aliases {
			routes[strings.ToLower(a)] = r
		}
		if rr, ok := reverbRoute(s); ok {
			routes[reverbHost(s)] = rr
		}
	}
	if r, ok := d.adminerRoute(); ok {
		routes[adminerHost] = r
	}
	return routes
}

// publicPort is the port the browser uses for a site, which PHP must see as SERVER_PORT.
func (d *Daemon) publicPort(s sites.Site) int {
	if s.HTTPS {
		return d.env.Cfg.Proxy.HTTPSPort
	}
	return d.env.Cfg.Proxy.HTTPPort
}

// serverPlan is what syncServer needs to bring an optional web server in line with its sites.
type serverPlan struct {
	service   string
	label     string
	adminPort int
	sites     int
	path      string  // folder of the version that should run
	running   *string // folder of the version that runs now (guarded by d.mu)
	write     func() error
	test      func() error
	reload    func() error
	spec      func() supervisor.Spec
}

// syncServer makes an optional web server serve exactly the sites that chose it: it starts with
// the first one (while clon's services are on), reloads when they change, restarts on another
// version and stops when none is left. prepErr is the error found while preparing the plan.
func (d *Daemon) syncServer(p serverPlan, prepErr error) error {
	pid := d.sup.PID(p.service)
	if p.sites == 0 {
		if pid != 0 {
			d.sup.Stop(p.service)
		}
		return nil
	}
	if prepErr != nil {
		return prepErr
	}
	d.mu.Lock()
	on, runningPath := d.servicesOn, *p.running
	d.mu.Unlock()
	if pid == 0 && !on {
		return nil
	}
	if err := p.write(); err != nil {
		return err
	}
	if err := p.test(); err != nil {
		return err
	}
	if pid != 0 && strings.EqualFold(runningPath, p.path) {
		return p.reload()
	}
	if pid != 0 {
		d.sup.Stop(p.service) // another version was installed or chosen
	}
	if err := ports.Check(p.adminPort); err != nil {
		return fmt.Errorf("%s no puede usar su puerto interno: %w", p.label, err)
	}
	spec := p.spec()
	spec.Env = append(spec.Env, d.env.ChildPATH())
	if err := d.sup.Start(spec); err != nil {
		return err
	}
	d.mu.Lock()
	*p.running = p.path
	d.mu.Unlock()
	return nil
}

// sitesOf lists the sites that use a server.
func (d *Daemon) sitesOf(server string) []sites.Site {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []sites.Site
	for _, s := range d.sites {
		if s.Server == server {
			out = append(out, s)
		}
	}
	return out
}

// ---- Nginx ----

func (d *Daemon) nginxVersion() (versions.Version, error) {
	v, ok := d.env.Reg.Find(versions.Nginx, d.env.Cfg.Nginx.Version)
	if !ok {
		return v, fmt.Errorf("Nginx no está instalado: instálalo en el panel (Servicios) o con 'clon service install nginx 1.30'")
	}
	return v, nil
}

func (d *Daemon) nginxOptions() (nginx.Options, error) {
	opt := nginx.Options{Home: d.env.Home, AdminPort: nginxAdminPort, Pools: d.nginxPools()}
	for _, s := range d.sitesOf(config.ServerNginx) {
		opt.Sites = append(opt.Sites, nginx.Site{Host: s.Host, Aliases: s.Aliases, DocRoot: s.DocRoot, Port: s.Port, HTTPS: s.HTTPS, PublicPort: d.publicPort(s), Pool: s.Pool})
	}
	v, err := d.nginxVersion()
	opt.Version = v
	return opt, err
}

func (d *Daemon) syncNginx() error {
	opt, err := d.nginxOptions()
	return d.syncServer(serverPlan{
		service: nginx.ServiceName, label: "Nginx", adminPort: nginxAdminPort, sites: len(opt.Sites),
		path: opt.Version.Path, running: &d.nginxPath,
		write:  func() error { _, err := nginx.Write(opt); return err },
		test:   func() error { return nginx.Test(d.env.Home, opt.Version) },
		reload: func() error { return nginx.Reload(d.env.Home, opt.Version) },
		spec:   func() supervisor.Spec { return nginx.Spec(opt) },
	}, err)
}

// ---- FrankenPHP ----

func (d *Daemon) frankenVersion() (versions.Version, error) {
	v, ok := d.env.Reg.Find(versions.FrankenPHP, d.env.Cfg.FrankenPHP.Version)
	if !ok {
		return v, fmt.Errorf("FrankenPHP no está instalado: instálalo en el panel (Servicios) o con 'clon service install frankenphp 1.13'")
	}
	return v, nil
}

// frankenPHPVersion describes the PHP built into a FrankenPHP version, so it gets its own php.ini
// (extensions, limits, CA bundle) like any other PHP, without clashing with a global PHP.
func frankenPHPVersion(v versions.Version) versions.Version {
	ts := true
	return versions.Version{Component: versions.PHP, Version: v.PHP + "-frankenphp-" + v.Version, Path: v.Path, ThreadSafe: &ts}
}

func (d *Daemon) frankenOptions() (frankenphp.Options, error) {
	opt := frankenphp.Options{Home: d.env.Home, AdminPort: frankenAdminPort}
	for _, s := range d.sitesOf(config.ServerFrankenPHP) {
		opt.Sites = append(opt.Sites, frankenphp.Site{Host: s.Host, DocRoot: s.DocRoot, Port: s.Port, HTTPS: s.HTTPS, PublicPort: d.publicPort(s)})
	}
	v, err := d.frankenVersion()
	opt.Version = v
	if err != nil || len(opt.Sites) == 0 {
		return opt, err
	}
	opts := d.env.IniOptions()
	opts.Builtin = true // FrankenPHP's PHP is built with its extensions inside
	ini, err := php.WriteIni(d.env.Home, frankenPHPVersion(v), opts)
	opt.IniDir = filepath.Dir(ini)
	return opt, err
}

func (d *Daemon) syncFrankenPHP() error {
	opt, err := d.frankenOptions()
	return d.syncServer(serverPlan{
		service: frankenphp.ServiceName, label: "FrankenPHP", adminPort: frankenAdminPort, sites: len(opt.Sites),
		path: opt.Version.Path, running: &d.frankenPath,
		write:  func() error { _, err := frankenphp.Write(opt); return err },
		test:   func() error { return frankenphp.Test(opt) },
		reload: func() error { return frankenphp.Reload(opt) },
		spec:   func() supervisor.Spec { return frankenphp.Spec(opt) },
	}, err)
}

// ---- Choosing a site's server ----

// SetSiteServer makes a site ("tienda" or "tienda.test") use Apache, Nginx or FrankenPHP. The site
// keeps its https://tienda.test address; only the server behind the proxy changes.
func (d *Daemon) SetSiteServer(site, server string) error {
	server = strings.ToLower(strings.TrimSpace(server))
	switch server {
	case config.ServerApache:
	case config.ServerNginx:
		if _, err := d.nginxVersion(); err != nil {
			return err
		}
	case config.ServerFrankenPHP:
		if _, err := d.frankenVersion(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("servidor desconocido %q: usa apache, nginx o frankenphp", server)
	}
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(site)), "."+d.env.Cfg.TLD)
	found := false
	d.mu.Lock()
	for _, s := range d.sites {
		found = found || s.Name == name
	}
	d.mu.Unlock()
	if !found {
		return fmt.Errorf("no hay ningún sitio %s.%s", name, d.env.Cfg.TLD)
	}
	d.cfgMu.Lock()
	d.env.Cfg.SetSiteServer(name, server)
	err := d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return err
	}
	err = d.Rescan()
	d.changed()
	return err
}
