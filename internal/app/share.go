package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// DefaultShareHours is how long a public link lives unless asked otherwise.
const DefaultShareHours = 8

// Share is a site published on the internet through a Cloudflare quick tunnel.
type Share struct {
	Site     string    `json:"site"`
	URL      string    `json:"url"` // https://<random>.trycloudflare.com
	Started  time.Time `json:"started"`
	Expires  time.Time `json:"expires"`
	Password bool      `json:"password"` // asks for a password
}

type shareState struct {
	Share
	host     string // the public host name, routed to the site
	siteHost string // tienda.test
	password string
	timer    *time.Timer
}

func shareService(site string) string { return "share-" + site }

var tunnelURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// cloudflared is the tunnel client, downloaded from Cloudflare's releases the first time.
func (d *Daemon) cloudflared(ctx context.Context) (versions.Version, error) {
	if v, ok := d.env.Reg.Find(versions.Cloudflared, ""); ok {
		return v, nil
	}
	list, err := install.Catalog(ctx, d.env.Home, versions.Cloudflared, false)
	if err != nil {
		return versions.Version{}, err
	}
	rel, err := install.FindRelease(list, "latest")
	if err != nil {
		return versions.Version{}, err
	}
	v, err := install.InstallRelease(ctx, d.env.Home, rel, nil, nil)
	if err != nil {
		return versions.Version{}, fmt.Errorf("no se pudo descargar cloudflared: %w", err)
	}
	d.cfgMu.Lock()
	d.env.Reg.Add(v)
	err = d.env.SaveRegistry()
	d.cfgMu.Unlock()
	return v, err
}

// ShareSite publishes a site at a temporary https://….trycloudflare.com address for some hours
// (DefaultShareHours when 0). With a password, the link asks for it. The tunnel reaches the site
// through Nido's proxy, which serves the public name with the site's backend: the project sees that
// name as its host, so the links it builds work from outside.
func (d *Daemon) ShareSite(site string, hours int, password string) (Share, error) {
	s, err := d.findSite(site)
	if err != nil {
		return Share{}, err
	}
	d.mu.Lock()
	if sh, ok := d.shares[s.Name]; ok {
		d.mu.Unlock()
		return sh.Share, nil
	}
	proxyUp := d.proxyUp
	d.mu.Unlock()
	if !proxyUp {
		return Share{}, fmt.Errorf("el proxy de Nido no está en marcha: inicia los servicios antes de compartir %s", s.Host)
	}
	if hours <= 0 {
		hours = DefaultShareHours
	}
	if hours > 72 {
		return Share{}, fmt.Errorf("un enlace dura como mucho 72 horas")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cf, err := d.cloudflared(ctx)
	if err != nil {
		return Share{}, err
	}
	origin := fmt.Sprintf("http://127.0.0.1:%d", d.env.Cfg.Proxy.HTTPPort)
	if s.HTTPS {
		origin = fmt.Sprintf("https://127.0.0.1:%d", d.env.Cfg.Proxy.HTTPSPort)
	}
	logPath := d.env.Home.Logs("share-" + s.Name + ".log")
	offset := int64(0)
	if st, err := os.Stat(logPath); err == nil {
		offset = st.Size()
	}
	name := shareService(s.Name)
	err = d.sup.Start(supervisor.Spec{
		Name: name, Exe: versions.Program(versions.Cloudflared, cf.Path), Dir: d.env.Home.Tmp(), LogPath: logPath,
		// A quick tunnel needs no Cloudflare account. It checks the site's certificate against Nido's CA.
		Args: []string{"tunnel", "--no-autoupdate", "--url", origin, "--origin-server-name", s.Host, "--origin-ca-pool", d.ca.CertPath()},
		StopTimeout: 5 * time.Second,
	})
	if err != nil {
		return Share{}, err
	}
	url, err := d.waitTunnelURL(name, logPath, offset, 60*time.Second)
	if err != nil {
		d.sup.Stop(name)
		return Share{}, err
	}
	now := time.Now()
	sh := &shareState{
		Share:    Share{Site: s.Host, URL: url, Started: now, Expires: now.Add(time.Duration(hours) * time.Hour), Password: password != ""},
		host:     strings.TrimPrefix(url, "https://"),
		siteHost: s.Host, password: password,
	}
	sh.timer = time.AfterFunc(time.Duration(hours)*time.Hour, func() { d.StopShare(s.Name) })
	d.mu.Lock()
	if d.shares == nil {
		d.shares = map[string]*shareState{}
	}
	d.shares[s.Name] = sh
	list := append([]sites.Site(nil), d.sites...)
	d.mu.Unlock()
	d.table.Set(d.routes(list))
	d.changed()
	return sh.Share, nil
}

// waitTunnelURL reads cloudflared's log until it prints the public address.
func (d *Daemon) waitTunnelURL(service, logPath string, offset int64, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var seen string
	for time.Now().Before(deadline) {
		if f, err := os.Open(logPath); err == nil {
			f.Seek(offset, io.SeekStart)
			data, _ := io.ReadAll(f)
			f.Close()
			seen = string(data)
			if u := tunnelURLRe.FindString(seen); u != "" {
				return u, nil
			}
		}
		if d.sup.PID(service) == 0 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "", fmt.Errorf("cloudflared no abrió el enlace (¿hay conexión a internet? Si tienes un config.yml en %%USERPROFILE%%\\.cloudflared, los enlaces rápidos no funcionan). Últimas líneas: %s", lastLines(seen, 4))
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, " | "))
}

// StopShare closes a site's public link.
func (d *Daemon) StopShare(site string) error {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(site)), "."+d.env.Cfg.TLD)
	d.mu.Lock()
	sh, ok := d.shares[name]
	if ok {
		delete(d.shares, name)
		sh.timer.Stop()
	}
	list := append([]sites.Site(nil), d.sites...)
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("%s.%s no está compartido", name, d.env.Cfg.TLD)
	}
	err := d.sup.Stop(shareService(name))
	d.table.Set(d.routes(list))
	d.changed()
	return err
}

// stopAllShares closes every public link (Nido's services are stopping).
func (d *Daemon) stopAllShares() {
	d.mu.Lock()
	var names []string
	for n := range d.shares {
		names = append(names, n)
	}
	d.mu.Unlock()
	for _, n := range names {
		d.StopShare(n)
	}
}

// Shares lists the open links.
func (d *Daemon) Shares() []Share {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []Share{}
	for _, sh := range d.shares {
		out = append(out, sh.Share)
	}
	return out
}

// shareOf is the open link of a site, nil when it has none.
func (d *Daemon) shareOf(site string) *Share {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sh, ok := d.shares[site]; ok {
		c := sh.Share
		return &c
	}
	return nil
}
