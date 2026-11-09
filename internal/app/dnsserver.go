package app

import (
	"time"

	"github.com/nicotc/laraclon/internal/dns"
)

// dnsAddr is where Windows' name resolution policy sends the local domain (it always uses port 53).
const dnsAddr = "127.0.0.1:53"

// DNSStatus is Nido's DNS server as the panel shows it.
type DNSStatus struct {
	Enabled bool `json:"enabled"` // [dns] enabled in nido.toml
	Running bool `json:"running"` // answering on 127.0.0.1:53
	Rule    bool `json:"rule"`    // Windows sends .<tld> to it
}

// startDNS opens the DNS server when it is enabled (it keeps running while Nido does, like the proxy).
func (d *Daemon) startDNS() error {
	if !d.env.Cfg.DNS.Enabled {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dnsSrv != nil && d.dnsSrv.Running() {
		return nil
	}
	srv := &dns.Server{TLD: d.env.Cfg.TLD, Addr: dnsAddr}
	if err := srv.Start(); err != nil {
		return err
	}
	d.dnsSrv = srv
	return nil
}

func (d *Daemon) stopDNS() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dnsSrv != nil {
		d.dnsSrv.Stop()
		d.dnsSrv = nil
	}
}

// DNSStatus reads the server and the Windows rule (cached for half a minute: reading it starts
// PowerShell).
func (d *Daemon) DNSStatus() DNSStatus {
	d.mu.Lock()
	st := DNSStatus{Enabled: d.env.Cfg.DNS.Enabled, Running: d.dnsSrv != nil && d.dnsSrv.Running()}
	if !st.Enabled {
		d.mu.Unlock()
		return st
	}
	fresh := time.Since(d.dnsRuleAt) < 30*time.Second
	rule := d.dnsRule
	d.mu.Unlock()
	if !fresh {
		rule = dns.RuleInstalled(d.env.Cfg.TLD)
		d.mu.Lock()
		d.dnsRule, d.dnsRuleAt = rule, time.Now()
		d.mu.Unlock()
	}
	st.Rule = rule
	return st
}

// dnsServes reports whether every name under the local domain already reaches this computer, so
// the hosts file needs no line per site.
func (d *Daemon) dnsServes() bool {
	st := d.DNSStatus()
	return st.Enabled && st.Running && st.Rule
}

// SetDNS turns the wildcard DNS on or off. The server opens first, so a busy port 53 is reported
// before Windows asks for permission; then the elevated helper adds or removes the rule.
func (d *Daemon) SetDNS(on bool) error {
	d.cfgMu.Lock()
	d.env.Cfg.DNS.Enabled = on
	err := d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return err
	}
	if on {
		if err := d.startDNS(); err != nil {
			d.cfgMu.Lock()
			d.env.Cfg.DNS.Enabled = false
			d.env.SaveConfig()
			d.cfgMu.Unlock()
			d.setProblem("dns", err)
			return err
		}
	}
	err = dns.RequestElevated(d.env.Home.Path("nido.exe"), d.env.Cfg.TLD, on)
	if !on {
		d.stopDNS()
	}
	d.mu.Lock()
	d.dnsRuleAt = time.Time{}
	d.mu.Unlock()
	d.setProblem("dns", err)
	if err == nil && on {
		d.startSiteProcesses() // Vite servers that were waiting for their name to resolve
	}
	d.changed()
	return err
}
