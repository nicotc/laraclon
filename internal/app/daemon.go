package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nicotc/laraclon/internal/apache"
	"github.com/nicotc/laraclon/internal/ca"
	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/frankenphp"
	"github.com/nicotc/laraclon/internal/hosts"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/nginx"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/proxy"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/vcredist"
	"github.com/nicotc/laraclon/internal/versions"
)

// Internal port plan: Apache's health-check vhost, then one port per site.
const (
	apacheAdminPort = 20000
	sitePortFrom    = 20001
	sitePortTo      = 29999
	// Sites that choose Nginx get a second, separate port, so moving a site between servers never
	// has to wait for one of them to release a port.
	nginxAdminPort   = 30000
	nginxPortFrom    = 30001
	nginxPortTo      = 39999
	frankenAdminPort = 40000
	frankenPortFrom  = 40001
	frankenPortTo    = 49999
	// Reverb, Laravel's WebSocket server, gets a port per site that runs it, kept below the ports
	// Windows hands out to outgoing connections and Hyper-V reserves (49152 and up).
	reverbPortFrom = 18001
	reverbPortTo   = 18999
)

// Daemon is the long-running part of clon: it owns the services, the front proxy and the API.
type Daemon struct {
	env   *Env
	sup   *supervisor.Supervisor
	ca    *ca.Authority
	table *proxy.Table
	front *proxy.Server
	store *sites.PortStore // Apache port of every site
	ngx   *sites.PortStore // Nginx port of the sites that use Nginx
	fphp  *sites.PortStore // FrankenPHP port of the sites that use FrankenPHP
	rvb   *sites.PortStore // Reverb port of the Laravel sites that require Reverb
	token string

	mu              sync.Mutex
	sites           []sites.Site
	warnings        []sites.Warning
	problems        map[string]string // component → last problem, shown in the panel and tray
	proxyUp         bool
	stopWatch       func()
	phpPorts        []int
	apacheConf      string
	controlPort     int
	trusted         bool
	trustedAt       time.Time
	servicesOn      bool   // between «Iniciar todo» and «Detener todo»
	nginxPath       string // folder of the nginx that is running
	frankenPath     string // folder of the FrankenPHP that is running
	runtime         RuntimeStatus
	runtimeAt       time.Time
	pools           []phpPool      // the global PHP pool first, then one per pinned version
	poolPorts       *poolStore     // ports of the pinned versions
	poolPortVersion map[int]string // pool each pinned pool port runs ("php-7-2-0", "php-8-4-16-debug")
	nodeNews        []install.NodeUpdate

	// cfgMu serialises changes to the service settings (panel, tray and installs run concurrently).
	cfgMu sync.Mutex
	jobs  jobList

	// OnChange is called after anything visible changes (tray menu refresh).
	OnChange func()
	quit     chan struct{}
}

// NewDaemon prepares everything that does not start processes yet.
func NewDaemon(env *Env) (*Daemon, error) {
	authority, created, err := ca.LoadOrCreate(env.Home.SSL())
	if err != nil {
		return nil, fmt.Errorf("CA local: %w", err)
	}
	if created || !fileExists(env.Home.SSL("cacert-bundle.pem")) {
		if err := authority.WriteBundle(install.MozillaBundle(env.Home), env.Home.SSL("cacert-bundle.pem")); err != nil {
			return nil, err
		}
	}
	store, err := sites.LoadPorts(env.Home.State("site-ports.json"))
	if err != nil {
		return nil, err
	}
	ngx, err := sites.LoadPorts(env.Home.State("site-ports-nginx.json"))
	if err != nil {
		return nil, err
	}
	fphp, err := sites.LoadPorts(env.Home.State("site-ports-frankenphp.json"))
	if err != nil {
		return nil, err
	}
	rvb, err := sites.LoadPorts(env.Home.State("site-ports-reverb.json"))
	if err != nil {
		return nil, err
	}
	pools, err := loadPoolStore(env.Home.State("php-pools.json"))
	if err != nil {
		return nil, err
	}
	tok := make([]byte, 24)
	rand.Read(tok)
	d := &Daemon{
		env:             env,
		sup:             supervisor.New(),
		ca:              authority,
		table:           &proxy.Table{},
		store:           store,
		ngx:             ngx,
		fphp:            fphp,
		rvb:             rvb,
		token:           hex.EncodeToString(tok),
		problems:        map[string]string{},
		quit:            make(chan struct{}),
		jobs:            jobList{jobs: map[string]*Job{}},
		poolPorts:       pools,
		poolPortVersion: map[int]string{},
	}
	d.sup.OnChange = d.changed
	return d, nil
}

func (d *Daemon) changed() {
	if d.OnChange != nil {
		d.OnChange()
	}
}

func (d *Daemon) setProblem(component string, err error) {
	d.mu.Lock()
	if err == nil {
		delete(d.problems, component)
	} else {
		d.problems[component] = err.Error()
	}
	d.mu.Unlock()
}

// Run starts the API and the services and blocks until ctx ends or Shutdown is called.
func (d *Daemon) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	ctl := &http.Server{Handler: d.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go ctl.Serve(ln)
	d.mu.Lock()
	d.controlPort = ln.Addr().(*net.TCPAddr).Port
	d.mu.Unlock()
	if err := writeControlFile(d.env.Home.Run("control.json"), Control{
		Port: d.controlPort, Token: d.token, PID: os.Getpid(),
	}); err != nil {
		return err
	}
	defer os.Remove(d.env.Home.Run("control.json"))

	d.StartAll()
	if stop, err := sites.Watch(d.env.Cfg.Roots, func() {
		if err := d.Rescan(); err != nil {
			d.setProblem("sitios", err)
		}
		d.changed()
	}); err == nil {
		d.stopWatch = stop
	} else {
		d.setProblem("sitios", err)
	}
	background, stopBackground := context.WithCancel(ctx)
	defer stopBackground()
	go d.watchNodeReleases(background)

	select {
	case <-ctx.Done():
	case <-d.quit:
	}
	if d.stopWatch != nil {
		d.stopWatch()
	}
	d.StopAll()
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if d.front != nil {
		d.front.Stop(stopCtx)
	}
	ctl.Shutdown(stopCtx)
	return nil
}

// Shutdown ends Run.
func (d *Daemon) Shutdown() {
	select {
	case <-d.quit:
	default:
		close(d.quit)
	}
}

// StartAll starts the databases, the PHP pool, the web servers and the front proxy, collecting
// problems instead of stopping at the first one.
func (d *Daemon) StartAll() {
	d.mu.Lock()
	d.servicesOn = true
	d.mu.Unlock()
	d.setProblem("visual-c++", d.runtimeProblem())
	if err := d.Rescan(); err != nil {
		d.setProblem("sitios", err)
	}
	d.cfgMu.Lock()
	d.startDatabases()
	d.cfgMu.Unlock()
	d.setProblem("php", d.startPHP())
	d.setProblem("apache", d.startApache())
	d.setProblem("nginx", d.syncNginx())
	d.setProblem("frankenphp", d.syncFrankenPHP())
	d.setProblem("adminer", d.startAdminer(false))
	d.setProblem("proxy", d.startProxy())
	d.startSiteProcesses() // each process reports its own problem
	d.changed()
}

// StopAll stops the services; the front proxy keeps answering with "server stopped" pages.
func (d *Daemon) StopAll() {
	d.mu.Lock()
	d.servicesOn = false
	d.mu.Unlock()
	d.stopAllProcesses()
	d.sup.Stop(adminerService)
	d.sup.Stop(nginx.ServiceName)
	d.sup.Stop(frankenphp.ServiceName)
	d.sup.Stop(apache.ServiceName)
	d.sup.StopMatching(func(n string) bool { return strings.HasPrefix(n, "php-cgi-") })
	d.sup.StopMatching(func(n string) bool { return strings.HasPrefix(n, "mysql-") || strings.HasPrefix(n, "mariadb-") })
	d.changed()
}

// RuntimeStatus is the Visual C++ runtime as the panel shows it.
type RuntimeStatus struct {
	Version string `json:"version"` // installed version, "" when missing
	Missing bool   `json:"missing"`
	Needs   string `json:"needs,omitempty"`   // runtime the global PHP or Apache was built for, when newer
	For     string `json:"for,omitempty"`     // that program, e.g. "Apache 2.4.69 (x64)"
	Restart bool   `json:"restart,omitempty"` // an update waits for Windows to restart
}

// runtimeStatus compares the runtime with what the global PHP and Apache were built for (both load
// it from Windows). It reads program headers, so the answer is kept for a few seconds.
func (d *Daemon) runtimeStatus() RuntimeStatus {
	d.mu.Lock()
	if time.Since(d.runtimeAt) < 10*time.Second {
		rt := d.runtime
		d.mu.Unlock()
		return rt
	}
	d.mu.Unlock()
	rt := RuntimeStatus{Version: vcredist.Version(), Missing: len(vcredist.Missing()) > 0}
	if !rt.Missing {
		var check []versions.Version
		if v, err := d.env.GlobalPHP(); err == nil {
			check = append(check, v)
		}
		if v, err := d.apacheVersion(); err == nil {
			check = append(check, v)
		}
		for _, v := range check {
			need := vcredist.Needs(versions.Program(v.Component, v.Path))
			if need != "" && vcredist.Older(need) && versions.Compare(need, rt.Needs) > 0 {
				rt.Needs, rt.For = need, v.Label()
			}
		}
		rt.Restart = rt.Needs != "" && vcredist.RestartPending()
	}
	d.mu.Lock()
	d.runtime, d.runtimeAt = rt, time.Now()
	d.mu.Unlock()
	return rt
}

// runtimeProblem is the runtime notice for the tray and 'clon status' (nil when all is well).
// Programs built for a newer runtime than Windows has usually run anyway (Apache Lounge's do), so
// that case is a recommendation.
func (d *Daemon) runtimeProblem() error {
	rt := d.runtimeStatus()
	switch {
	case rt.Missing:
		return fmt.Errorf("falta el runtime de Visual C++ x64: sin él no arrancan PHP, Apache ni MySQL. Instálalo desde el panel o con 'clon vcredist install'")
	case rt.Needs != "" && rt.Restart:
		return fmt.Errorf("el runtime de Visual C++ que pide %s ya está instalado; Windows lo termina de aplicar al reiniciar", rt.For)
	case rt.Needs != "":
		return fmt.Errorf("%s se compiló para el runtime de Visual C++ %s o más nuevo y Windows tiene %s; Microsoft recomienda actualizarlo desde el panel o con 'clon vcredist install'", rt.For, rt.Needs, rt.Version)
	}
	return nil
}

// InstallRuntime installs Microsoft's latest Visual C++ runtime (Windows asks for permission) and
// then starts whatever could not start without it.
func (d *Daemon) InstallRuntime(ctx context.Context) error {
	err := vcredist.Install(ctx, d.env.Home, "", nil, nil)
	d.mu.Lock()
	d.runtimeAt = time.Time{}
	on := d.servicesOn
	d.mu.Unlock()
	if errors.Is(err, vcredist.ErrRestart) {
		err = nil // the notice tells that Windows finishes the update at the next restart
	}
	if err != nil {
		return err
	}
	if on {
		d.StartAll()
	}
	d.setProblem("visual-c++", d.runtimeProblem())
	d.changed()
	return nil
}

func (d *Daemon) startPHP() error {
	v, err := d.env.GlobalPHP()
	if err != nil {
		return err
	}
	ini, err := php.WriteIni(d.env.Home, v, d.env.IniOptions())
	if err != nil {
		return err
	}
	portsList := php.PoolPorts(d.env.Cfg.PHP.BasePort, d.env.Cfg.PHP.PoolSize)
	d.mu.Lock()
	d.phpPorts = portsList
	d.mu.Unlock()
	var errs []string
	for _, spec := range php.PoolSpecs(d.env.Home, v, ini, portsList) {
		if d.sup.PID(spec.Name) != 0 {
			continue
		}
		spec.Env = append(spec.Env, d.env.ChildPATH())
		if err := ports.Check(spec.Port); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if err := d.sup.Start(spec); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (d *Daemon) apacheVersion() (versions.Version, error) {
	v, ok := d.env.Reg.Find(versions.Apache, d.env.Cfg.Apache.Version)
	if !ok {
		return v, fmt.Errorf("no hay ninguna versión de Apache instalada")
	}
	return v, nil
}

func (d *Daemon) apacheOptions() (apache.Options, error) {
	v, err := d.apacheVersion()
	if err != nil {
		return apache.Options{}, err
	}
	opt := apache.Options{Home: d.env.Home, Version: v, AdminPort: apacheAdminPort, Pools: d.apachePools()}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.sites {
		if s.Server != config.ServerApache {
			continue
		}
		opt.Sites = append(opt.Sites, apache.Site{Host: s.Host, Aliases: s.Aliases, DocRoot: s.DocRoot, Port: s.Port, HTTPS: s.HTTPS, Pool: s.Pool})
	}
	return opt, nil
}

func (d *Daemon) startApache() error {
	opt, err := d.apacheOptions()
	if err != nil {
		return err
	}
	if d.sup.PID(apache.ServiceName) != 0 {
		return nil
	}
	if err := ports.Check(opt.AdminPort); err != nil {
		return fmt.Errorf("Apache no puede usar su puerto interno: %w", err)
	}
	conf, err := apache.Write(opt)
	if err != nil {
		return err
	}
	if err := apache.Test(opt.Version, conf); err != nil {
		return err
	}
	d.mu.Lock()
	d.apacheConf = conf
	d.mu.Unlock()
	spec := apache.Spec(opt, conf)
	spec.Env = append(spec.Env, d.env.ChildPATH())
	return d.sup.Start(spec)
}

func (d *Daemon) startProxy() error {
	d.mu.Lock()
	up := d.proxyUp
	d.mu.Unlock()
	if up {
		return nil
	}
	addrs := []string{"127.0.0.1", "::1"}
	if d.env.Cfg.Proxy.LAN {
		addrs = []string{"0.0.0.0", "::"}
	}
	for _, p := range []int{d.env.Cfg.Proxy.HTTPPort, d.env.Cfg.Proxy.HTTPSPort} {
		if err := ports.Check(p); err != nil {
			return fmt.Errorf("el proxy no puede abrir sus puertos: %w. Cierra ese programa o cambia [proxy] en clon.toml", err)
		}
	}
	d.front = &proxy.Server{
		Table: d.table, CA: d.ca, Panel: d.Handler(), TLD: d.env.Cfg.TLD,
		HTTPPort: d.env.Cfg.Proxy.HTTPPort, HTTPSPort: d.env.Cfg.Proxy.HTTPSPort, Addrs: addrs,
	}
	if err := d.front.Start(); err != nil {
		return err
	}
	d.mu.Lock()
	d.proxyUp = true
	d.mu.Unlock()
	return nil
}

// Rescan re-reads the project roots, keeps ports stable, reconfigures the web servers and updates
// the proxy.
func (d *Daemon) Rescan() error {
	list, warns := sites.Scan(d.env.Cfg)
	// Ports clon's own servers hold right now are not conflicts.
	own := map[string]map[int]bool{}
	d.mu.Lock()
	for _, s := range d.sites {
		if own[s.Server] == nil {
			own[s.Server] = map[int]bool{}
		}
		own[s.Server][s.Port] = true
	}
	d.mu.Unlock()
	if err := assignPorts(list, d.store, d.serverPorts(), own); err != nil {
		return err
	}
	if err := d.assignReverbPorts(list); err != nil {
		return err
	}
	pools, notes := d.planPHP(list)
	d.mu.Lock()
	d.sites, d.warnings, d.pools = list, append(warns, notes...), pools
	d.mu.Unlock()
	d.stopOrphanProcesses(list)

	// The pool of a pinned PHP runs before any server sends it requests, and stops after none does.
	d.setProblem("php-fijado", d.startPinnedPools(pools))
	defer d.stopUnusedPools(pools)
	// Reloaded servers answer the next request with their new configuration.
	defer proxy.ForgetConnections()

	// The optional servers first: a site moving to one is served there before the proxy points to it.
	d.setProblem("nginx", d.syncNginx())
	d.setProblem("frankenphp", d.syncFrankenPHP())
	d.table.Set(d.routes(list))

	// A running Apache picks up the new sites with a graceful restart (no dropped requests).
	if pid := d.sup.PID(apache.ServiceName); pid != 0 {
		opt, err := d.apacheOptions()
		if err != nil {
			return err
		}
		conf, err := apache.Write(opt)
		if err != nil {
			return err
		}
		if err := apache.Test(opt.Version, conf); err != nil {
			return err
		}
		return apache.GracefulRestart(pid)
	}
	return nil
}

// UsePHP switches the global PHP: its pool restarts on the same ports, Apache is not touched. The
// pools of the versions some sites pinned keep running.
func (d *Daemon) UsePHP(want string) (versions.Version, error) {
	v, ok := d.env.Reg.Find(versions.PHP, want)
	if !ok {
		return v, fmt.Errorf("PHP %s no está instalado", want)
	}
	d.env.Cfg.PHP.Version = v.Version
	if err := d.env.SaveConfig(); err != nil {
		return v, err
	}
	if _, err := d.env.RefreshShims(); err != nil {
		return v, err
	}
	global := map[string]bool{}
	for _, port := range php.PoolPorts(d.env.Cfg.PHP.BasePort, d.env.Cfg.PHP.PoolSize) {
		global[php.PoolName(port)] = true
	}
	d.sup.StopMatching(func(n string) bool { return global[n] })
	err := d.startPHP()
	d.setProblem("php", err)
	if d.sup.PID(adminerService) != 0 {
		d.setProblem("adminer", d.startAdminer(true)) // Adminer runs on the global PHP
	}
	// A site that pinned the old or the new global version moves between pools.
	if d.anyPinned() {
		if err := d.Rescan(); err != nil {
			d.setProblem("sitios", err)
		}
	}
	d.restartPHPProcesses() // queue workers and schedulers follow the new PHP
	d.changed()
	return v, err
}

// UseApache switches the Apache that serves the sites. A running Apache restarts with the new
// version (a few seconds without answering); the sites keep their ports.
func (d *Daemon) UseApache(want string) (versions.Version, error) {
	v, ok := d.env.Reg.Find(versions.Apache, want)
	if !ok {
		return v, fmt.Errorf("Apache %s no está instalado", want)
	}
	d.cfgMu.Lock()
	d.env.Cfg.Apache.Version = v.Version
	err := d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return v, err
	}
	running := d.sup.PID(apache.ServiceName) != 0
	if running {
		d.sup.Stop(apache.ServiceName)
	}
	d.mu.Lock()
	list := append([]sites.Site(nil), d.sites...)
	on := d.servicesOn
	d.mu.Unlock()
	d.table.Set(d.routes(list)) // the proxy's pages name the server
	d.setProblem("visual-c++", d.runtimeProblem())
	if running || on {
		err = d.startApache()
		if err == nil {
			// Answer once the sites are served again, so a switch never reports success early.
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err = d.sup.WaitRunning(ctx, apache.ServiceName)
			cancel()
		}
		d.setProblem("apache", err)
	}
	d.changed()
	return v, err
}

// TrustCA asks Windows to trust the local CA for the current user (Windows shows a confirmation).
func (d *Daemon) TrustCA() error {
	err := d.ca.Trust()
	d.mu.Lock()
	d.trustedAt = time.Time{}
	d.mu.Unlock()
	d.changed()
	return err
}

// SyncHosts writes the site names to the managed hosts block through the elevated helper.
func (d *Daemon) SyncHosts() error {
	names := d.hostNames()
	current, err := os.ReadFile(hosts.Path())
	if err != nil {
		return err
	}
	if hosts.InSync(string(current), names) {
		return nil
	}
	list := d.env.Home.Run("hosts-names.txt")
	if err := config.WriteFileAtomic(list, []byte(strings.Join(names, "\r\n"))); err != nil {
		return err
	}
	err = hosts.RequestElevated(d.env.Home.Path("clon.exe"), list)
	if err == nil {
		d.startSiteProcesses() // Vite servers that were waiting for their site's line in hosts
	}
	d.changed()
	return err
}

func (d *Daemon) hostNames() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var names []string
	for _, s := range d.sites {
		names = append(names, s.Host)
		names = append(names, s.Aliases...)
		if s.Reverb {
			names = append(names, reverbHost(s))
		}
	}
	return names
}

// SiteURL is how the browser reaches a site through the proxy.
func (d *Daemon) SiteURL(s sites.Site) string {
	if s.HTTPS {
		if d.env.Cfg.Proxy.HTTPSPort == 443 {
			return "https://" + s.Host + "/"
		}
		return fmt.Sprintf("https://%s:%d/", s.Host, d.env.Cfg.Proxy.HTTPSPort)
	}
	if d.env.Cfg.Proxy.HTTPPort == 80 {
		return "http://" + s.Host + "/"
	}
	return fmt.Sprintf("http://%s:%d/", s.Host, d.env.Cfg.Proxy.HTTPPort)
}

// PanelURL is the address of the panel: https://localhost once Windows trusts the local CA,
// otherwise the plain local control address, so the panel never opens on a certificate warning.
func (d *Daemon) PanelURL() string {
	d.mu.Lock()
	port := d.controlPort
	d.mu.Unlock()
	if !d.CATrusted() && port != 0 {
		return fmt.Sprintf("http://127.0.0.1:%d/", port)
	}
	if d.env.Cfg.Proxy.HTTPSPort == 443 {
		return "https://localhost/"
	}
	return fmt.Sprintf("https://localhost:%d/", d.env.Cfg.Proxy.HTTPSPort)
}

// CATrusted reports (cached for a few seconds) whether Windows trusts the local CA.
func (d *Daemon) CATrusted() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Since(d.trustedAt) > 10*time.Second {
		d.trusted = d.ca.Trusted()
		d.trustedAt = time.Now()
	}
	return d.trusted
}

// Control is written to run\control.json so the command line can find the running daemon.
type Control struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func writeControlFile(path string, c Control) error {
	data, _ := json.Marshal(c)
	return config.WriteFileAtomic(path, data)
}

// ReadControl loads run\control.json.
func ReadControl(home string) (Control, error) {
	var c Control
	data, err := os.ReadFile(filepath.Join(home, "run", "control.json"))
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(data, &c)
}
