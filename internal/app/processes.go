package app

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// Project processes are the long-running helpers of a site that clon supervises like a service:
// Vite's dev server, Laravel's queue worker and its scheduler.
var projectProcesses = []string{"vite", "queue", "schedule"}

var processLabels = map[string]string{"vite": "Vite", "queue": "Cola", "schedule": "Tareas"}

// processService is the supervisor name of a site's process ("vite-tienda").
func processService(id, site string) string { return id + "-" + site }

func isProcessService(name string) bool {
	for _, id := range projectProcesses {
		if strings.HasPrefix(name, id+"-") {
			return true
		}
	}
	return false
}

// ProcessStatus is one process of a site as the panel shows it.
type ProcessStatus struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	On    bool   `json:"on"` // runs, and starts with clon's services
	State string `json:"state"`
}

// siteProcesses are the processes a site can run: Vite when it has a Vite config, the queue and the
// scheduler for Laravel.
func siteProcesses(s sites.Site) []string {
	var out []string
	if s.Vite {
		out = append(out, "vite")
	}
	if s.Kind == "laravel" {
		out = append(out, "queue", "schedule")
	}
	return out
}

func (d *Daemon) processStatuses(s sites.Site) []ProcessStatus {
	on := map[string]bool{}
	for _, id := range d.env.Cfg.Sites[s.Name].Processes {
		on[id] = true
	}
	var out []ProcessStatus
	for _, id := range siteProcesses(s) {
		st := ProcessStatus{ID: id, Label: processLabels[id], On: on[id], State: "detenido"}
		if d.sup.PID(processService(id, s.Name)) != 0 {
			st.State = "en marcha"
		}
		out = append(out, st)
	}
	return out
}

// sitePHP is the PHP a site's Artisan commands run with: the version it pinned, or the global one.
func (d *Daemon) sitePHP(s sites.Site) (versions.Version, error) {
	if s.PHPVersion != "" {
		if v, ok := d.env.Reg.Find(versions.PHP, s.PHPVersion); ok {
			return v, nil
		}
	}
	return d.env.GlobalPHP()
}

// viteEnv makes Vite's dev server answer at https://<site>:5173 with the site's certificate, which
// Windows trusts through clon's CA. laravel-vite-plugin reads these variables (also from .env).
func (d *Daemon) viteEnv(s sites.Site) ([]string, error) {
	if _, err := d.ca.Leaf(s.Host); err != nil {
		return nil, err
	}
	cert, key := d.ca.SiteFiles(s.Host)
	return []string{"VITE_DEV_SERVER_KEY=" + key, "VITE_DEV_SERVER_CERT=" + cert}, nil
}

// processSpec describes one process of a site.
func (d *Daemon) processSpec(s sites.Site, id string) (supervisor.Spec, error) {
	spec := supervisor.Spec{
		Name: processService(id, s.Name), Dir: s.Folder, Restart: true, StopTimeout: 10 * time.Second,
		LogPath: d.env.Home.Logs(s.Name + "-" + id + ".log"),
	}
	switch id {
	case "vite":
		// Vite listens on the site's name; without its line in hosts it would just exit.
		if addrs, err := net.LookupHost(s.Host); err != nil || len(addrs) == 0 {
			return spec, fmt.Errorf("Vite escucha en %s, que todavía no está en el archivo hosts: actualízalo desde el panel («Actualizar hosts») o con 'clon hosts sync'", s.Host)
		}
		node, err := d.env.GlobalNode()
		if err != nil {
			return spec, err
		}
		env, err := d.viteEnv(s)
		if err != nil {
			return spec, err
		}
		spec.Exe = filepath.Join(node.Path, "node.exe")
		spec.Args = []string{filepath.Join(node.Path, "node_modules", "npm", "bin", "npm-cli.js"), "run", "dev"}
		// The plugin takes the server's name from APP_URL, which must not carry the proxy's port.
		// Vite then listens on the site's name (its line in hosts, which the browser needs anyway):
		// a --host option would make the pages load from an address the certificate does not cover.
		spec.Env = append(env, d.env.ChildPATH(), "NPM_CONFIG_PREFIX="+d.env.NPMPrefix(), "APP_URL=https://"+s.Host)
	case "queue", "schedule":
		v, err := d.sitePHP(s)
		if err != nil {
			return spec, err
		}
		ini, err := php.WriteIni(d.env.Home, v, d.env.IniOptions())
		if err != nil {
			return spec, err
		}
		cmd := []string{"queue:work", "--tries=3"}
		if id == "schedule" {
			cmd = []string{"schedule:work"}
		}
		spec.Exe = filepath.Join(v.Path, "php.exe")
		spec.Args = append([]string{"-c", ini, "artisan"}, cmd...)
		spec.Env = []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH()}
	default:
		return spec, fmt.Errorf("proceso desconocido %q: usa vite, queue o schedule", id)
	}
	return spec, nil
}

func (d *Daemon) findSite(site string) (sites.Site, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(site)), "."+d.env.Cfg.TLD)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.sites {
		if s.Name == name {
			return s, nil
		}
	}
	return sites.Site{}, fmt.Errorf("no hay ningún sitio %s.%s", name, d.env.Cfg.TLD)
}

// SetSiteProcess runs or stops a process of a site, and remembers it so it starts with clon's
// services. Turning Vite on also writes its certificate to the project's .env, so `npm run dev` in a
// terminal serves HTTPS too.
func (d *Daemon) SetSiteProcess(site, id string, on bool) error {
	s, err := d.findSite(site)
	if err != nil {
		return err
	}
	allowed := false
	for _, p := range siteProcesses(s) {
		allowed = allowed || p == id
	}
	if !allowed {
		return fmt.Errorf("%s no puede usar %q (Vite necesita vite.config y package.json; la cola y las tareas, Laravel)", s.Host, id)
	}
	d.cfgMu.Lock()
	d.env.Cfg.SetSiteProcess(s.Name, id, on)
	err = d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return err
	}
	name := processService(id, s.Name)
	if !on {
		err = d.stopProcess(s, id)
		d.setProblem(name, nil)
		d.changed()
		return err
	}
	if id == "vite" {
		if env, err := d.viteEnv(s); err == nil {
			var values [][2]string
			for _, kv := range env {
				k, v, _ := strings.Cut(kv, "=")
				values = append(values, [2]string{k, v})
			}
			editEnvFile(filepath.Join(s.Folder, ".env"), values) // a project without .env still runs Vite through clon
		}
	}
	err = d.startProcess(s, id)
	d.setProblem(name, err)
	d.changed()
	return err
}

// viteHot is the file laravel-vite-plugin keeps in public\ while its dev server runs. Stopped with
// Ctrl+C the plugin removes it; ended by clon it stays, and Laravel would keep pointing the pages at
// a server that is gone.
func viteHot(s sites.Site) string { return filepath.Join(s.Folder, "public", "hot") }

// stopProcess stops one process of a site, cleaning up after Vite.
func (d *Daemon) stopProcess(s sites.Site, id string) error {
	name := processService(id, s.Name)
	running := d.sup.PID(name) != 0
	err := d.sup.Stop(name)
	if id == "vite" && running {
		os.Remove(viteHot(s))
	}
	return err
}

// stopAllProcesses stops every project process (clon's services are stopping).
func (d *Daemon) stopAllProcesses() {
	d.mu.Lock()
	list := append([]sites.Site(nil), d.sites...)
	d.mu.Unlock()
	for _, s := range list {
		for _, id := range projectProcesses {
			if d.sup.PID(processService(id, s.Name)) != 0 {
				d.stopProcess(s, id)
			}
		}
	}
	d.sup.StopMatching(isProcessService) // processes of sites that are gone
}

func (d *Daemon) startProcess(s sites.Site, id string) error {
	name := processService(id, s.Name)
	if d.sup.PID(name) != 0 {
		return nil
	}
	spec, err := d.processSpec(s, id)
	if err != nil {
		return err
	}
	return d.sup.Start(spec)
}

// startSiteProcesses starts the processes the sites keep on, when clon's services start.
func (d *Daemon) startSiteProcesses() error {
	d.mu.Lock()
	list := append([]sites.Site(nil), d.sites...)
	d.mu.Unlock()
	var errs []string
	for _, s := range list {
		allowed := map[string]bool{}
		for _, id := range siteProcesses(s) {
			allowed[id] = true
		}
		for _, id := range d.env.Cfg.Sites[s.Name].Processes {
			if !allowed[id] {
				continue
			}
			err := d.startProcess(s, id)
			d.setProblem(processService(id, s.Name), err)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s de %s: %v", processLabels[id], s.Host, err))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// stopOrphanProcesses stops the processes of sites that are gone (their folder was removed).
func (d *Daemon) stopOrphanProcesses(list []sites.Site) {
	keep := map[string]bool{}
	for _, s := range list {
		for _, id := range projectProcesses {
			keep[processService(id, s.Name)] = true
		}
	}
	d.sup.StopMatching(func(n string) bool { return isProcessService(n) && !keep[n] })
}

// restartPHPProcesses restarts the running queue and scheduler processes, after a PHP change.
func (d *Daemon) restartPHPProcesses() {
	d.mu.Lock()
	list := append([]sites.Site(nil), d.sites...)
	d.mu.Unlock()
	for _, s := range list {
		for _, id := range []string{"queue", "schedule"} {
			name := processService(id, s.Name)
			if d.sup.PID(name) == 0 {
				continue
			}
			d.sup.Stop(name)
			d.setProblem(name, d.startProcess(s, id))
		}
	}
}
