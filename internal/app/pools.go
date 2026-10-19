package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/apache"
	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/nginx"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

// PHP pools: the global PHP has one; every other version some site pinned gets a small one of its
// own, on ports right after the global pool's range (base_port+100 …).
const (
	pinnedPoolSize   = 2
	pinnedPoolOffset = 100
	pinnedPoolSpan   = 100
)

// phpPool is one group of php-cgi processes and the sites it serves.
type phpPool struct {
	Name    string
	Version versions.Version
	Ports   []int
	Global  bool
	Sites   int
}

// PoolStatus is one PHP pool as the panel shows it.
type PoolStatus struct {
	Version string `json:"version"`
	Global  bool   `json:"global"`
	Ports   []int  `json:"ports"`
	Sites   int    `json:"sites"`
}

func poolName(version string) string { return "php-" + strings.ReplaceAll(version, ".", "-") }

// poolStore keeps the ports of each pinned version stable across restarts (state\php-pools.json).
type poolStore struct {
	path  string
	Ports map[string]int `json:"ports"` // version → first port of its block
}

func loadPoolStore(path string) (*poolStore, error) {
	s := &poolStore{path: path, Ports: map[string]int{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Ports == nil {
		s.Ports = map[string]int{}
	}
	return s, nil
}

func (s *poolStore) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(s.path, data)
}

// assign returns the ports of a pinned version: its previous block when still usable, otherwise
// the first free block in [from, to]. own are ports held by clon's own pools right now.
func (s *poolStore) assign(version string, from, to int, own map[int]bool) ([]int, error) {
	block := func(base int) []int {
		out := make([]int, pinnedPoolSize)
		for i := range out {
			out[i] = base + i
		}
		return out
	}
	taken := map[int]bool{}
	for v, base := range s.Ports {
		if v != version {
			for _, p := range block(base) {
				taken[p] = true
			}
		}
	}
	usable := func(base int) bool {
		for _, p := range block(base) {
			if p < from || p > to || taken[p] || (!own[p] && ports.Check(p) != nil) {
				return false
			}
		}
		return true
	}
	if base, ok := s.Ports[version]; ok && usable(base) {
		return block(base), nil
	}
	for base := from; base+pinnedPoolSize-1 <= to; base += pinnedPoolSize {
		if usable(base) {
			s.Ports[version] = base
			return block(base), nil
		}
	}
	return nil, fmt.Errorf("no quedan puertos libres para otro PHP fijado (%d-%d)", from, to)
}

// forget drops the blocks of versions that are no longer installed.
func (s *poolStore) forget(installed map[string]bool) {
	for v := range s.Ports {
		if !installed[v] {
			delete(s.Ports, v)
		}
	}
}

// planPools decides which pool serves each site and returns the pools, the global one first. Sites
// served by Apache or Nginx go to the global pool unless they pinned another installed version;
// FrankenPHP brings its own PHP. It fills in Site.Pool and Site.PHPVersion and returns a note for
// every pin it cannot honour.
func planPools(list []sites.Site, reg *versions.Registry, global versions.Version, globalPorts []int,
	store *poolStore, from, to int, own map[int]bool) ([]phpPool, []sites.Warning) {
	pools := []phpPool{{Name: apache.GlobalPool, Version: global, Ports: globalPorts, Global: true}}
	var notes []sites.Warning
	index := map[string]int{}
	installed := map[string]bool{}
	for _, v := range reg.List(versions.PHP) {
		installed[v.Version] = true
	}
	store.forget(installed)
	for i := range list {
		s := &list[i]
		s.Pool, s.PHPVersion = "", ""
		if s.Server == config.ServerFrankenPHP {
			continue
		}
		s.Pool, s.PHPVersion = apache.GlobalPool, global.Version
		target := 0
		if s.PHP != "" {
			v, ok := reg.Find(versions.PHP, s.PHP)
			switch {
			case !ok:
				notes = append(notes, sites.Warning{Folder: s.Folder, Reason: fmt.Sprintf("%s fija PHP %s, que no está instalado; usa el PHP global", s.Host, s.PHP)})
			case v.Version == global.Version:
				// The global PHP already is that version.
			default:
				idx, seen := index[v.Version]
				if !seen {
					ports, err := store.assign(v.Version, from, to, own)
					if err != nil {
						notes = append(notes, sites.Warning{Folder: s.Folder, Reason: err.Error()})
						break
					}
					pools = append(pools, phpPool{Name: poolName(v.Version), Version: v, Ports: ports})
					idx = len(pools) - 1
					index[v.Version] = idx
				}
				target = idx
			}
		}
		pools[target].Sites++
		s.Pool, s.PHPVersion = pools[target].Name, pools[target].Version.Version
	}
	return pools, notes
}

// planPHP plans the pools for a fresh list of sites (see planPools).
func (d *Daemon) planPHP(list []sites.Site) ([]phpPool, []sites.Warning) {
	global, _ := d.env.GlobalPHP()
	d.mu.Lock()
	globalPorts := d.phpPorts
	own := map[int]bool{}
	for _, p := range d.pools {
		if !p.Global {
			for _, port := range p.Ports {
				own[port] = true
			}
		}
	}
	d.mu.Unlock()
	if len(globalPorts) == 0 {
		globalPorts = php.PoolPorts(d.env.Cfg.PHP.BasePort, d.env.Cfg.PHP.PoolSize)
	}
	from := d.env.Cfg.PHP.BasePort + pinnedPoolOffset
	pools, notes := planPools(list, d.env.Reg, global, globalPorts, d.poolPorts, from, from+pinnedPoolSpan-1, own)
	if err := d.poolPorts.save(); err != nil {
		notes = append(notes, sites.Warning{Reason: err.Error()})
	}
	return pools, notes
}

// startPinnedPools starts the processes of the pinned pools that are not running yet (while clon's
// services are on) and waits a few seconds for them, so a site moved to its pool answers at once.
// The global pool is startPHP's.
func (d *Daemon) startPinnedPools(pools []phpPool) error {
	d.mu.Lock()
	on := d.servicesOn
	d.mu.Unlock()
	if !on {
		return nil
	}
	var started []string
	var errs []string
	for _, p := range pools {
		if p.Global {
			continue
		}
		ini, err := php.WriteIni(d.env.Home, p.Version, d.env.IniOptions())
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		for _, spec := range php.PoolSpecs(d.env.Home, p.Version, ini, p.Ports) {
			if d.sup.PID(spec.Name) != 0 {
				if d.poolVersionOn(spec.Port) == p.Version.Version {
					continue
				}
				d.sup.Stop(spec.Name) // the port changed hands
			}
			if err := ports.Check(spec.Port); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			spec.Env = append(spec.Env, d.env.ChildPATH())
			if err := d.sup.Start(spec); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			d.mu.Lock()
			d.poolPortVersion[spec.Port] = p.Version.Version
			d.mu.Unlock()
			started = append(started, spec.Name)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, name := range started {
		if err := d.sup.WaitRunning(ctx, name); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("PHP fijado: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (d *Daemon) poolVersionOn(port int) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.poolPortVersion[port]
}

// stopUnusedPools stops php-cgi processes that belong to no pool any more: a site went back to the
// global PHP, or the global PHP became the version it pinned.
func (d *Daemon) stopUnusedPools(pools []phpPool) {
	keep := map[string]bool{}
	for _, p := range pools {
		for _, port := range p.Ports {
			keep[php.PoolName(port)] = true
		}
	}
	d.sup.StopMatching(func(n string) bool { return strings.HasPrefix(n, "php-cgi-") && !keep[n] })
}

// apachePools and nginxPools describe the planned pools to each web server.
func (d *Daemon) apachePools() []apache.Pool {
	var out []apache.Pool
	for _, p := range d.currentPools() {
		out = append(out, apache.Pool{Name: p.Name, Ports: p.Ports})
	}
	return out
}

func (d *Daemon) nginxPools() []nginx.Pool {
	var out []nginx.Pool
	for _, p := range d.currentPools() {
		out = append(out, nginx.Pool{Name: p.Name, Ports: p.Ports})
	}
	return out
}

// currentPools are the planned pools, or the global one alone before the first plan.
func (d *Daemon) currentPools() []phpPool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.pools) > 0 {
		return append([]phpPool(nil), d.pools...)
	}
	globalPorts := d.phpPorts
	if len(globalPorts) == 0 {
		globalPorts = php.PoolPorts(d.env.Cfg.PHP.BasePort, d.env.Cfg.PHP.PoolSize)
	}
	return []phpPool{{Name: apache.GlobalPool, Ports: globalPorts, Global: true}}
}

// poolStatuses lists the pools for the panel: the global one first, then the pinned ones by version.
func (d *Daemon) poolStatuses() []PoolStatus {
	pools := d.currentPools()
	var out []PoolStatus
	for _, p := range pools {
		out = append(out, PoolStatus{Version: p.Version.Version, Global: p.Global, Ports: p.Ports, Sites: p.Sites})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Global != out[j].Global {
			return out[i].Global
		}
		return versions.Compare(out[i].Version, out[j].Version) > 0
	})
	return out
}

// SetSitePHP pins a site ("tienda" or "tienda.test") to a PHP version, or back to the global PHP with
// "" or "global". Terminals inside the site's folder follow it too (php and composer).
func (d *Daemon) SetSitePHP(site, want string) error {
	want = strings.TrimSpace(want)
	if strings.EqualFold(want, "global") {
		want = ""
	}
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(site)), "."+d.env.Cfg.TLD)
	var found *sites.Site
	d.mu.Lock()
	for i := range d.sites {
		if d.sites[i].Name == name {
			s := d.sites[i]
			found = &s
		}
	}
	d.mu.Unlock()
	if found == nil {
		return fmt.Errorf("no hay ningún sitio %s.%s", name, d.env.Cfg.TLD)
	}
	if found.Server == config.ServerFrankenPHP && want != "" {
		return fmt.Errorf("%s usa FrankenPHP, que trae su propio PHP; pásalo a Apache o Nginx para fijarle otro", found.Host)
	}
	if want != "" {
		v, ok := d.env.Reg.Find(versions.PHP, want)
		if !ok {
			return fmt.Errorf("PHP %s no está instalado (instálalo con 'clon php install %s')", want, versions.MajorMinor(want))
		}
		// Composer for the terminal inside the project: 2.2 LTS below PHP 7.2.5.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := d.env.EnsureComposerFor(ctx, v.Version)
		cancel()
		d.setProblem("composer", err)
	}
	d.cfgMu.Lock()
	d.env.Cfg.SetSitePHP(name, want)
	err := d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return err
	}
	d.refreshShims()
	err = d.Rescan()
	d.changed()
	return err
}

// anyPinned reports whether some site pinned its own PHP.
func (d *Daemon) anyPinned() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.sites {
		if s.PHP != "" {
			return true
		}
	}
	return false
}
