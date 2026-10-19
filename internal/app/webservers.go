package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/nginx"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/proxy"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

// assignPorts gives every site its Apache port (kept even while it uses Nginx, so switching back
// finds the same one) and the Nginx sites their Nginx port. Site.Port ends up as the port of the
// server the site uses, which is where the proxy sends its requests.
func assignPorts(list []sites.Site, apacheStore, nginxStore *sites.PortStore, ownApache, ownNginx map[int]bool) error {
	if err := apacheStore.Assign(list, sitePortFrom, sitePortTo, ownApache); err != nil {
		return err
	}
	if err := apacheStore.Save(); err != nil {
		return err
	}
	var ng []sites.Site
	for _, s := range list {
		if s.Server == config.ServerNginx {
			s.Port = 0
			ng = append(ng, s)
		}
	}
	if len(ng) == 0 {
		return nil
	}
	if err := nginxStore.Assign(ng, nginxPortFrom, nginxPortTo, ownNginx); err != nil {
		return err
	}
	if err := nginxStore.Save(); err != nil {
		return err
	}
	byName := map[string]int{}
	for _, s := range ng {
		byName[s.Name] = s.Port
	}
	for i := range list {
		if p, ok := byName[list[i].Name]; ok {
			list[i].Port = p
		}
	}
	return nil
}

// routes maps every host and alias to the internal port of the site's server.
func (d *Daemon) routes(list []sites.Site) map[string]proxy.Route {
	labels := map[string]string{config.ServerApache: "Apache", config.ServerNginx: "Nginx"}
	if v, err := d.apacheVersion(); err == nil {
		labels[config.ServerApache] = "Apache " + v.Version
	}
	if v, err := d.nginxVersion(); err == nil {
		labels[config.ServerNginx] = "Nginx " + v.Version
	}
	routes := map[string]proxy.Route{}
	for _, s := range list {
		backend, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.Port))
		r := proxy.Route{Site: s.Host, Server: labels[s.Server], Backend: backend, HTTPS: s.HTTPS}
		routes[s.Host] = r
		for _, a := range s.Aliases {
			routes[strings.ToLower(a)] = r
		}
	}
	return routes
}

func (d *Daemon) nginxVersion() (versions.Version, error) {
	v, ok := d.env.Reg.Find(versions.Nginx, d.env.Cfg.Nginx.Version)
	if !ok {
		return v, fmt.Errorf("Nginx no está instalado: instálalo en el panel (Servicios) o con 'clon service install nginx 1.30'")
	}
	return v, nil
}

// publicPort is the port the browser uses for a site, which PHP must see as SERVER_PORT.
func (d *Daemon) publicPort(s sites.Site) int {
	if s.HTTPS {
		return d.env.Cfg.Proxy.HTTPSPort
	}
	return d.env.Cfg.Proxy.HTTPPort
}

func (d *Daemon) nginxOptions() (nginx.Options, error) {
	d.mu.Lock()
	opt := nginx.Options{Home: d.env.Home, AdminPort: nginxAdminPort, PHPPorts: d.phpPorts}
	for _, s := range d.sites {
		if s.Server == config.ServerNginx {
			opt.Sites = append(opt.Sites, nginx.Site{Host: s.Host, Aliases: s.Aliases, DocRoot: s.DocRoot, Port: s.Port, HTTPS: s.HTTPS, PublicPort: d.publicPort(s)})
		}
	}
	d.mu.Unlock()
	if len(opt.PHPPorts) == 0 {
		opt.PHPPorts = php.PoolPorts(d.env.Cfg.PHP.BasePort, d.env.Cfg.PHP.PoolSize)
	}
	v, err := d.nginxVersion()
	opt.Version = v
	return opt, err
}

// syncNginx makes nginx serve exactly the sites that chose it: it starts with the first one (while
// clon's services are on), reloads when they change and stops when none is left.
func (d *Daemon) syncNginx() error {
	opt, err := d.nginxOptions()
	pid := d.sup.PID(nginx.ServiceName)
	if len(opt.Sites) == 0 {
		if pid != 0 {
			d.sup.Stop(nginx.ServiceName)
		}
		return nil
	}
	if err != nil {
		return err
	}
	d.mu.Lock()
	on, runningPath := d.servicesOn, d.nginxPath
	d.mu.Unlock()
	if pid == 0 && !on {
		return nil
	}
	if _, err := nginx.Write(opt); err != nil {
		return err
	}
	if err := nginx.Test(d.env.Home, opt.Version); err != nil {
		return err
	}
	if pid != 0 && strings.EqualFold(runningPath, opt.Version.Path) {
		return nginx.Reload(d.env.Home, opt.Version)
	}
	if pid != 0 {
		d.sup.Stop(nginx.ServiceName) // another version was installed or chosen
	}
	if err := ports.Check(opt.AdminPort); err != nil {
		return fmt.Errorf("Nginx no puede usar su puerto interno: %w", err)
	}
	spec := nginx.Spec(opt)
	spec.Env = append(spec.Env, d.env.ChildPATH())
	if err := d.sup.Start(spec); err != nil {
		return err
	}
	d.mu.Lock()
	d.nginxPath = opt.Version.Path
	d.mu.Unlock()
	return nil
}

// SetSiteServer makes a site ("tienda" or "tienda.test") use Apache or Nginx. The site keeps its
// https://tienda.test address; only the server behind the proxy changes.
func (d *Daemon) SetSiteServer(site, server string) error {
	server = strings.ToLower(strings.TrimSpace(server))
	if server != config.ServerApache && server != config.ServerNginx {
		return fmt.Errorf("servidor desconocido %q: usa apache o nginx", server)
	}
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(site)), "."+d.env.Cfg.TLD)
	found := false
	d.mu.Lock()
	for _, s := range d.sites {
		if s.Name == name {
			found = true
		}
	}
	d.mu.Unlock()
	if !found {
		return fmt.Errorf("no hay ningún sitio %s.%s", name, d.env.Cfg.TLD)
	}
	if server == config.ServerNginx {
		if _, err := d.nginxVersion(); err != nil {
			return err
		}
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
