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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nicotc/laraclon/internal/apache"
	"github.com/nicotc/laraclon/internal/ca"
	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/hosts"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/mysql"
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
)

// Daemon is the long-running part of clon: it owns the services, the front proxy and the API.
type Daemon struct {
	env   *Env
	sup   *supervisor.Supervisor
	ca    *ca.Authority
	table *proxy.Table
	front *proxy.Server
	store *sites.PortStore
	token string

	mu          sync.Mutex
	sites       []sites.Site
	warnings    []sites.Warning
	problems    map[string]string // component → last problem, shown in the panel and tray
	proxyUp     bool
	stopWatch   func()
	phpPorts    []int
	apacheConf  string
	controlPort int
	trusted     bool
	trustedAt   time.Time

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
	tok := make([]byte, 24)
	rand.Read(tok)
	d := &Daemon{
		env:      env,
		sup:      supervisor.New(),
		ca:       authority,
		table:    &proxy.Table{},
		store:    store,
		token:    hex.EncodeToString(tok),
		problems: map[string]string{},
		quit:     make(chan struct{}),
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

// StartAll starts MySQL, the PHP pool, Apache and the front proxy, collecting problems instead of
// stopping at the first one.
func (d *Daemon) StartAll() {
	if missing := vcredist.Missing(); len(missing) > 0 {
		d.setProblem("visual-c++", fmt.Errorf("falta el runtime de Visual C++ 2015-2022 x64 (%s): sin él no arrancan PHP, Apache ni MySQL. Instálalo con 'clon vcredist install' y luego 'clon import laragon'", strings.Join(missing, ", ")))
	} else {
		d.setProblem("visual-c++", nil)
	}
	if err := d.Rescan(); err != nil {
		d.setProblem("sitios", err)
	}
	d.setProblem("mysql", d.startMySQL())
	d.setProblem("php", d.startPHP())
	d.setProblem("apache", d.startApache())
	d.setProblem("proxy", d.startProxy())
	d.changed()
}

// StopAll stops the services; the front proxy keeps answering with "server stopped" pages.
func (d *Daemon) StopAll() {
	d.sup.Stop(apache.ServiceName)
	d.sup.StopMatching(func(n string) bool { return strings.HasPrefix(n, "php-cgi-") })
	d.sup.StopMatching(func(n string) bool { return strings.HasPrefix(n, "mysql-") || strings.HasPrefix(n, "mariadb-") })
	d.changed()
}

func (d *Daemon) mysqlInstance() (*mysql.Instance, error) {
	if !d.env.Cfg.MySQL.Enabled {
		return nil, nil
	}
	v, err := d.env.MySQLVersion()
	if err != nil {
		return nil, err
	}
	c := d.env.Cfg.MySQL
	if c.DataDir != "" && c.DataDirVersion != "" && c.DataDirVersion != v.Version {
		return nil, fmt.Errorf("la carpeta de datos %s es de MySQL %s; abrirla con MySQL %s la actualizaría y la versión anterior (Laragon) ya no podría usarla. Pon [mysql] version = %q o quita data_dir", c.DataDir, c.DataDirVersion, v.Version, c.DataDirVersion)
	}
	return &mysql.Instance{Home: d.env.Home, Version: v, Port: c.Port, External: c.DataDir}, nil
}

func (d *Daemon) startMySQL() error {
	inst, err := d.mysqlInstance()
	if err != nil || inst == nil {
		return err
	}
	if d.sup.PID(inst.ServiceName()) != 0 {
		return nil
	}
	if pid, exe, busy := inst.InUseBy(); busy {
		return fmt.Errorf("otro MySQL ya usa la carpeta de datos %s: %s (PID %d). Detenlo (por ejemplo, cierra Laragon) antes de iniciar el de clon", inst.DataDir(), exe, pid)
	}
	if inst.External != "" && !inst.Initialized() {
		return fmt.Errorf("%s no contiene datos de MySQL", inst.DataDir())
	}
	if err := ports.Check(inst.Port); err != nil {
		return fmt.Errorf("MySQL no puede usar su puerto: %w. Cambia [mysql] port en clon.toml", err)
	}
	if err := inst.WriteIni(); err != nil {
		return err
	}
	if err := inst.Initialize(); err != nil {
		return err
	}
	return d.sup.Start(inst.Spec())
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
	d.mu.Lock()
	defer d.mu.Unlock()
	opt := apache.Options{Home: d.env.Home, Version: v, AdminPort: apacheAdminPort, PHPPorts: d.phpPorts}
	if len(opt.PHPPorts) == 0 {
		opt.PHPPorts = php.PoolPorts(d.env.Cfg.PHP.BasePort, d.env.Cfg.PHP.PoolSize)
	}
	for _, s := range d.sites {
		opt.Sites = append(opt.Sites, apache.Site{Host: s.Host, Aliases: s.Aliases, DocRoot: s.DocRoot, Port: s.Port, HTTPS: s.HTTPS})
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

// Rescan re-reads the project roots, keeps ports stable, updates the proxy and reloads Apache.
func (d *Daemon) Rescan() error {
	list, warns := sites.Scan(d.env.Cfg)
	own := map[int]bool{}
	d.mu.Lock()
	for _, s := range d.sites {
		own[s.Port] = true
	}
	d.mu.Unlock()
	if err := d.store.Assign(list, sitePortFrom, sitePortTo, own); err != nil {
		return err
	}
	if err := d.store.Save(); err != nil {
		return err
	}
	d.mu.Lock()
	d.sites, d.warnings = list, warns
	d.mu.Unlock()

	serverLabel := "Apache"
	if v, err := d.apacheVersion(); err == nil {
		serverLabel = "Apache " + v.Version
	}
	routes := map[string]proxy.Route{}
	for _, s := range list {
		backend, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.Port))
		r := proxy.Route{Site: s.Host, Server: serverLabel, Backend: backend, HTTPS: s.HTTPS}
		routes[s.Host] = r
		for _, a := range s.Aliases {
			routes[strings.ToLower(a)] = r
		}
	}
	d.table.Set(routes)

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

// UsePHP switches the global PHP: the pool restarts on the same ports, Apache is not touched.
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
	d.sup.StopMatching(func(n string) bool { return strings.HasPrefix(n, "php-cgi-") })
	err := d.startPHP()
	d.setProblem("php", err)
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
