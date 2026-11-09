package app

import (
	"strings"

	"github.com/nicotc/laraclon/internal/dumps"
	"github.com/nicotc/laraclon/internal/versions"
)

// DumpsView is what the «Dumps» tab shows.
type DumpsView struct {
	On      bool           `json:"on"`      // dump() and dd() reach Nido
	Page    bool           `json:"page"`    // and the page still shows them
	Running bool           `json:"running"` // the receiver has its port open
	Last    int64          `json:"last"`    // sequence number of the newest dump, to ask only for news
	Groups  []dumps.Group  `json:"groups"`
}

// startDumps opens the receiver of dump() and dd() (it runs while Nido does).
func (d *Daemon) startDumps() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dumpSrv != nil {
		return nil
	}
	srv := &dumps.Server{Store: d.dumps}
	if err := srv.Start(); err != nil {
		return err
	}
	d.dumpSrv = srv
	return nil
}

func (d *Daemon) stopDumps() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dumpSrv != nil {
		d.dumpSrv.Stop()
		d.dumpSrv = nil
	}
}

// Dumps lists the dumps by request; with since, only when something newer arrived (Groups is then
// empty if nothing did, so the panel can poll cheaply).
func (d *Daemon) Dumps(since int64) DumpsView {
	d.mu.Lock()
	running := d.dumpSrv != nil
	d.mu.Unlock()
	v := DumpsView{On: !d.env.Cfg.Dumps.Off, Page: !d.env.Cfg.Dumps.Quiet, Running: running, Last: d.dumps.Last(), Groups: []dumps.Group{}}
	if since > 0 && since >= v.Last {
		return v
	}
	v.Groups = dumps.Groups(d.dumps.All())
	// A command's dumps name the site whose folder it ran in.
	d.mu.Lock()
	for i, g := range v.Groups {
		if g.Host != "" || g.Cwd == "" {
			continue
		}
		cwd := strings.ToLower(g.Cwd) + `\`
		for _, s := range d.sites {
			if strings.HasPrefix(cwd, strings.ToLower(s.Folder)+`\`) {
				v.Groups[i].Host = s.Host
				break
			}
		}
	}
	d.mu.Unlock()
	return v
}

// ClearDumps forgets every dump.
func (d *Daemon) ClearDumps() {
	d.dumps.Clear()
	d.changed()
}

// SetDumps turns the capture on or off and chooses whether the page keeps showing the dumps. The
// page setting lives in the prepended file alone (the next request reads it); turning the capture
// on or off changes php.ini, so the running php-cgi restart.
func (d *Daemon) SetDumps(on, page bool) error {
	d.cfgMu.Lock()
	changed := d.env.Cfg.Dumps.Off == on
	d.env.Cfg.Dumps.Off, d.env.Cfg.Dumps.Quiet = !on, !page
	err := d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return err
	}
	d.env.DumpPrepend()
	if !changed {
		d.changed()
		return nil
	}
	d.refreshShims() // every php.ini, with or without auto_prepend_file
	var errs []string
	for _, v := range d.env.Reg.List(versions.PHP) {
		if err := d.restartPHPVersion(v); err != nil {
			errs = append(errs, err.Error())
		}
	}
	d.restartPHPProcesses()
	d.changed()
	if len(errs) > 0 {
		return &multiError{errs}
	}
	return nil
}

type multiError struct{ list []string }

func (m *multiError) Error() string { return strings.Join(m.list, "; ") }
