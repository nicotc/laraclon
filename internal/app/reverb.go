package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/nicotc/laraclon/internal/proxy"
	"github.com/nicotc/laraclon/internal/sites"
)

// Reverb, Laravel's WebSocket server, runs per site on an internal port and is published through the
// proxy at reverb.<site>.test, with the site's certificate: the browser (Echo) and PHP (broadcast)
// both reach wss://reverb.<site>.test, and Reverb itself needs no TLS.

// reverbHost is the name a site's Reverb answers at.
func reverbHost(s sites.Site) string { return "reverb." + s.Host }

// assignReverbPorts gives the sites that require Reverb a stable internal port for it.
func (d *Daemon) assignReverbPorts(list []sites.Site) error {
	// The ports of the Reverb servers running now are not conflicts.
	d.mu.Lock()
	prev := append([]sites.Site(nil), d.sites...)
	d.mu.Unlock()
	own := map[int]bool{}
	for _, s := range prev {
		if s.ReverbPort != 0 && d.sup.PID(processService("reverb", s.Name)) != 0 {
			own[s.ReverbPort] = true
		}
	}
	var subset []sites.Site
	for _, s := range list {
		if s.Reverb {
			s.Port = 0
			subset = append(subset, s)
		}
	}
	if len(subset) == 0 {
		return nil
	}
	if err := d.rvb.Assign(subset, reverbPortFrom, reverbPortTo, own); err != nil {
		return err
	}
	if err := d.rvb.Save(); err != nil {
		return err
	}
	byName := map[string]int{}
	for _, s := range subset {
		byName[s.Name] = s.Port
	}
	for i := range list {
		list[i].ReverbPort = byName[list[i].Name]
	}
	return nil
}

// reverbRoute sends reverb.<site>.test to the site's Reverb server.
func reverbRoute(s sites.Site) (proxy.Route, bool) {
	if !s.Reverb || s.ReverbPort == 0 {
		return proxy.Route{}, false
	}
	backend, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.ReverbPort))
	return proxy.Route{Site: reverbHost(s), Server: "Reverb", Backend: backend, HTTPS: s.HTTPS}, true
}

// reverbEnv are the .env values that point Laravel and Echo (VITE_REVERB_* repeats them) at the site's
// Reverb through the proxy, and make Reverb listen on its internal port.
func (d *Daemon) reverbEnv(s sites.Site) [][2]string {
	scheme, port := "https", d.env.Cfg.Proxy.HTTPSPort
	if !s.HTTPS {
		scheme, port = "http", d.env.Cfg.Proxy.HTTPPort
	}
	return [][2]string{
		{"REVERB_HOST", reverbHost(s)}, {"REVERB_PORT", fmt.Sprint(port)}, {"REVERB_SCHEME", scheme},
		{"REVERB_SERVER_HOST", "127.0.0.1"}, {"REVERB_SERVER_PORT", fmt.Sprint(s.ReverbPort)},
	}
}

// useReverb writes a site's Reverb settings to its .env. When they change and the frontend was
// compiled with the old ones (Echo takes them at build time) and Vite is not serving it live, the
// frontend is compiled again in the background.
func (d *Daemon) useReverb(s sites.Site) error {
	envFile := filepath.Join(s.Folder, ".env")
	values := d.reverbEnv(s)
	changed := false
	for _, kv := range values {
		changed = changed || envValueOf(envFile, kv[0]) != kv[1]
	}
	if !changed {
		return nil
	}
	if err := editEnvFile(envFile, values); err != nil {
		return err
	}
	if !fileExists(filepath.Join(s.Folder, "public", "build", "manifest.json")) || d.sup.PID(processService("vite", s.Name)) != 0 {
		return nil
	}
	_, err := d.startProjectJob(s.Name, "Reverb", "reverb-"+s.Name, "recompilación cancelada",
		func(ctx context.Context, phase func(string), log *os.File) error {
			pm := projectManager(s.Folder)
			run, err := d.ensureJSRunner(ctx, pm, s.Folder, phase, log)
			if err != nil {
				return err
			}
			phase("compilando el frontend para " + reverbHost(s))
			env := []string{d.env.ChildPATH(), "NPM_CONFIG_PREFIX=" + d.env.NPMPrefix()}
			if err := run.run(ctx, s.Folder, env, log, "run", "build"); err != nil {
				return fmt.Errorf("%s run build: %w", pm, err)
			}
			return nil
		})
	return err
}
