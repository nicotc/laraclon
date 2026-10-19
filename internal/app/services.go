package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/versions"
	"github.com/nicotc/laraclon/internal/winx"
)

// DBGroup is one kind of database with its instances, as the panel shows it.
type DBGroup struct {
	Kind      string           `json:"kind"`
	Name      string           `json:"name"`
	Default   string           `json:"default"`
	Instances []InstanceStatus `json:"instances"`
}

// InstanceStatus is one database instance with its settings and state.
type InstanceStatus struct {
	Key       string `json:"key"`
	Series    string `json:"series"`
	Version   string `json:"version"`
	Source    string `json:"source"`
	Port      int    `json:"port"`
	Autostart bool   `json:"autostart"`
	Default   bool   `json:"default"`
	State     string `json:"state"`
	PID       int    `json:"pid,omitempty"`
	DataDir   string `json:"data_dir"`
	External  bool   `json:"external"`
	Error     string `json:"error,omitempty"`
	WebURL    string `json:"web_url,omitempty"` // Mailpit's inbox, Meilisearch's dashboard
}

// dbGroups describes every kind of database for the status.
func (d *Daemon) dbGroups(problems map[string]string) []DBGroup {
	byName := map[string]supervisorState{}
	for _, s := range d.sup.Statuses() {
		byName[s.Name] = supervisorState{state: string(s.State), pid: s.PID, err: s.LastError}
	}
	var out []DBGroup
	for _, kind := range ServiceKinds {
		svc := d.env.Cfg.Service(string(kind))
		// Instances is never null: the panel counts them even for a kind nothing of is installed.
		g := DBGroup{Kind: string(kind), Name: kind.Name(), Default: svc.Default, Instances: []InstanceStatus{}}
		for _, ref := range d.env.Instances(kind) {
			key := ref.Key()
			st := InstanceStatus{
				Key: key, Series: ref.Series, Version: ref.Version.Version, Source: ref.Version.Source,
				Port: ref.Instance.Port, Autostart: ref.Instance.Autostart || ref.Series == svc.Default,
				Default: ref.Series == svc.Default, State: "detenido", External: ref.Instance.DataDir != "",
				DataDir: ref.Instance.DataDir, WebURL: webURL(kind, ref.Instance.Port),
			}
			if st.DataDir == "" {
				st.DataDir = d.env.Home.Data(key)
			}
			if s, ok := byName[key]; ok {
				st.State, st.PID = s.state, s.pid
				if s.state == "error" {
					st.Error = s.err
				}
			}
			if msg, ok := problems[key]; ok {
				st.Error = msg
			}
			g.Instances = append(g.Instances, st)
		}
		out = append(out, g)
	}
	return out
}

type supervisorState struct {
	state, err string
	pid        int
}

// startDB starts one instance: writes its configuration, creates its data the first time and
// launches it under the supervisor.
func (d *Daemon) startDB(kind versions.Component, series string) error {
	r, err := d.runnerFor(kind, series)
	if err != nil {
		return err
	}
	if d.sup.PID(r.Key()) != 0 {
		return nil
	}
	if x, ok := r.(externalData); ok {
		if pid, exe, busy := x.InUseBy(); busy {
			return fmt.Errorf("otro servidor ya usa la carpeta de datos %s: %s (PID %d). Detenlo (o cierra el programa que lo abrió) antes de iniciar el de Nido", r.DataDir(), exe, pid)
		}
		if x.ExternalDir() != "" && !x.Initialized() {
			return fmt.Errorf("%s no contiene datos de MySQL", r.DataDir())
		}
	}
	for _, p := range r.Ports() {
		if err := ports.Check(p); err != nil {
			return fmt.Errorf("%s no puede usar su puerto: %w. Cámbialo en el panel (Servicios) o con 'nido service port %s <puerto>'", r.Key(), err, r.Key())
		}
	}
	if err := r.Prepare(); err != nil {
		return err
	}
	if err := r.Initialize(); err != nil {
		return err
	}
	return d.sup.Start(r.Spec())
}

// startDatabases starts the default instance of each kind and the ones marked to start with
// «Iniciar todo». Each instance reports its own problem.
func (d *Daemon) startDatabases() {
	for _, kind := range ServiceKinds {
		svc := d.env.Cfg.Service(string(kind))
		for _, ref := range d.env.Instances(kind) {
			if ref.Series == svc.Default || ref.Instance.Autostart {
				d.setProblem(ref.Key(), d.startDB(kind, ref.Series))
			}
		}
	}
}

// StartService starts one database instance ("mysql-8.4").
func (d *Daemon) StartService(key string) error {
	kind, series, err := ParseInstanceKey(key)
	if err != nil {
		return err
	}
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	err = d.startDB(kind, series)
	d.setProblem(key, err)
	d.changed()
	return err
}

// StopService stops one database instance.
func (d *Daemon) StopService(key string) error {
	if _, _, err := ParseInstanceKey(key); err != nil {
		return err
	}
	err := d.sup.Stop(key)
	d.setProblem(key, nil)
	d.changed()
	return err
}

// restartDB stops and starts an instance that was running, reporting its problem.
func (d *Daemon) restartDB(key string) error {
	kind, series, _ := ParseInstanceKey(key)
	d.sup.Stop(key)
	err := d.startDB(kind, series)
	d.setProblem(key, err)
	return err
}

// ServiceSettings changes the port and/or the «Iniciar todo» flag of an instance. A running
// instance restarts on its new port.
func (d *Daemon) ServiceSettings(key string, port *int, autostart *bool) error {
	kind, series, err := ParseInstanceKey(key)
	if err != nil {
		return err
	}
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	svc := d.env.Cfg.Service(string(kind))
	inst, ok := svc.Instances[series]
	if !ok {
		return fmt.Errorf("%s no existe", key)
	}
	restart := false
	if port != nil && *port != inst.Port {
		if err := d.env.CheckPort(key, *port); err != nil {
			return err
		}
		if site := d.siteOnPort(*port); site != "" {
			return fmt.Errorf("el puerto %d lo usa el sitio %s", *port, site)
		}
		inst.Port = *port
		restart = d.sup.PID(key) != 0
	}
	if autostart != nil {
		inst.Autostart = *autostart
	}
	svc.Instances[series] = inst
	d.env.Cfg.SetService(string(kind), svc)
	if err := d.env.SaveConfig(); err != nil {
		return err
	}
	d.refreshShims()
	if restart {
		err = d.restartDB(key)
	}
	d.changed()
	return err
}

// siteOnPort is the site whose web server listens on port ("" when none does).
func (d *Daemon) siteOnPort(port int) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.sites {
		if s.Port == port {
			return s.Host
		}
	}
	return ""
}

// SetDefaultService makes an instance the default of its kind. When the previous default had the
// standard port they swap ports, so projects keep finding "their" database on 3306; instances that
// were running restart on their new ports.
func (d *Daemon) SetDefaultService(key string) error {
	kind, series, err := ParseInstanceKey(key)
	if err != nil {
		return err
	}
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	svc := d.env.Cfg.Service(string(kind))
	inst, ok := svc.Instances[series]
	if !ok {
		return fmt.Errorf("%s no existe", key)
	}
	old := svc.Default
	if old == series {
		return nil
	}
	oldKey := InstanceKey(kind, old)
	oldWasRunning := old != "" && d.sup.PID(oldKey) != 0
	var restart []string
	if oldInst, ok := svc.Instances[old]; ok && oldInst.Port == StandardPort(kind) {
		oldInst.Port, inst.Port = inst.Port, oldInst.Port
		svc.Instances[old] = oldInst
		for _, k := range []string{oldKey, key} {
			if d.sup.PID(k) != 0 {
				restart = append(restart, k)
			}
		}
	}
	svc.Instances[series] = inst
	svc.Default = series
	d.env.Cfg.SetService(string(kind), svc)
	if err := d.env.SaveConfig(); err != nil {
		return err
	}
	d.refreshShims()
	// Both stop before either starts: they are trading ports.
	for _, k := range restart {
		d.sup.Stop(k)
	}
	var errs []string
	for _, k := range restart {
		kk, ss, _ := ParseInstanceKey(k)
		err := d.startDB(kk, ss)
		d.setProblem(k, err)
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	// The default always runs while Nido's services do.
	if oldWasRunning && d.sup.PID(key) == 0 {
		err := d.startDB(kind, series)
		d.setProblem(key, err)
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	d.changed()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// UninstallService removes the installed versions of an instance's series. Downloaded files are
// deleted; versions Nido did not download are only forgotten. The data folder is always kept.
func (d *Daemon) UninstallService(key string) error {
	kind, series, err := ParseInstanceKey(key)
	if err != nil {
		return err
	}
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	d.sup.Stop(key)
	binRoot := strings.ToLower(d.env.Home.Bin()) + `\`
	for _, v := range d.env.Reg.List(kind) {
		if versions.Series(kind, v.Version) != series {
			continue
		}
		if v.Source == "download" && strings.HasPrefix(strings.ToLower(filepath.Clean(v.Path))+`\`, binRoot) {
			if err := os.RemoveAll(v.Path); err != nil {
				return fmt.Errorf("no se pudo borrar %s: %w", v.Path, err)
			}
		}
		d.env.Reg.Remove(kind, v.Version)
	}
	svc := d.env.Cfg.Service(string(kind))
	removed := svc.Instances[series]
	delete(svc.Instances, series)
	if svc.Default == series {
		svc.Default = ""
		if rest := d.env.SeriesInstalled(kind); len(rest) > 0 {
			svc.Default = rest[0]
			// The new default takes over the standard port when it can do so without a restart.
			next := svc.Instances[rest[0]]
			if removed.Port == StandardPort(kind) && d.sup.PID(InstanceKey(kind, rest[0])) == 0 {
				next.Port = StandardPort(kind)
				svc.Instances[rest[0]] = next
			}
		}
	}
	d.env.Cfg.SetService(string(kind), svc)
	if err := d.env.SaveRegistry(); err != nil {
		return err
	}
	if err := d.env.SaveConfig(); err != nil {
		return err
	}
	d.refreshShims()
	d.setProblem(key, nil)
	d.changed()
	return nil
}

// OpenPath shows an instance's log in the default text editor or its data folder in Explorer.
func (d *Daemon) OpenPath(key, what string) error {
	kind, series, err := ParseInstanceKey(key)
	if err != nil {
		return err
	}
	r, err := d.runnerFor(kind, series)
	if err != nil {
		return err
	}
	target := r.DataDir()
	if what == "log" {
		target = r.LogPath()
	}
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("todavía no existe %s (se crea al iniciar %s por primera vez)", target, key)
	}
	return winx.OpenURL(target)
}

func (d *Daemon) refreshShims() {
	if _, err := d.env.RefreshShims(); err != nil {
		log.Println("atajos:", err)
	}
}

// ---- Install jobs ----

// Job is an installation running in the background; the panel polls it to show progress.
type Job struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Series   string    `json:"series"`
	Version  string    `json:"version"`
	Phase    string    `json:"phase"`
	Done     int64     `json:"done"`
	Total    int64     `json:"total"`
	Error    string    `json:"error,omitempty"`
	Note     string    `json:"note,omitempty"` // shown when it ends well (a generated password, for example)
	Finished bool      `json:"finished"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitempty"`

	cancel context.CancelFunc
}

type jobList struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func (l *jobList) update(j *Job, fn func(*Job)) {
	l.mu.Lock()
	fn(j)
	l.mu.Unlock()
}

// Catalog lists the versions of a component that can be installed.
func (d *Daemon) Catalog(ctx context.Context, kind versions.Component, refresh bool) ([]install.Release, error) {
	if !installable(kind) {
		return nil, fmt.Errorf("%s no se puede instalar desde el catálogo", kind)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return install.Catalog(ctx, d.env.Home, kind, refresh)
}

func installable(kind versions.Component) bool {
	for _, k := range install.Installable {
		if k == kind {
			return true
		}
	}
	return false
}

// InstallService starts installing a series ("8.4") or version from the catalog.
func (d *Daemon) InstallService(kind versions.Component, want string) (Job, error) {
	list, err := d.Catalog(context.Background(), kind, false)
	if err != nil {
		return Job{}, err
	}
	rel, err := install.FindRelease(list, want)
	if err != nil {
		return Job{}, err
	}
	return d.startInstall(rel, false)
}

// startInstall installs a release in the background; use makes it the version in use once
// installed (Node).
func (d *Daemon) startInstall(rel install.Release, use bool) (Job, error) {
	if v, ok := d.env.Reg.Find(rel.Kind, rel.Version); ok && v.Version == rel.Version {
		return Job{}, fmt.Errorf("%s ya está instalado", v.Label())
	}
	d.jobs.mu.Lock()
	defer d.jobs.mu.Unlock()
	for _, j := range d.jobs.jobs {
		if !j.Finished && j.Kind == string(rel.Kind) && j.Series == rel.Series {
			return *j, nil
		}
	}
	id := make([]byte, 6)
	rand.Read(id)
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{ID: hex.EncodeToString(id), Kind: string(rel.Kind), Series: rel.Series, Version: rel.Version,
		Phase: "preparando", Total: rel.Size, Started: time.Now(), cancel: cancel}
	d.jobs.jobs[j.ID] = j
	go d.runInstall(ctx, j, rel, use)
	return *j, nil
}

func (d *Daemon) runInstall(ctx context.Context, j *Job, rel install.Release, use bool) {
	defer j.cancel()
	v, err := install.InstallRelease(ctx, d.env.Home, rel,
		func(phase string) { d.jobs.update(j, func(j *Job) { j.Phase = phase }) },
		func(done, total int64) {
			d.jobs.update(j, func(j *Job) {
				j.Done = done
				if total > 0 {
					j.Total = total
				}
			})
		})
	isDefault, nodeSwitched := false, false
	if err == nil {
		d.cfgMu.Lock()
		d.env.Reg.Add(v)
		err = d.env.SaveRegistry()
		// Saved before the job ends: 'nido service install' exits as soon as it sees the job finished.
		if rel.Kind == versions.Apache {
			d.env.Cfg.Apache.Version = v.Version
		}
		// A downloaded Node becomes the global one when asked to, or when the global one is not a
		// Node Nido downloaded (none yet, or one found elsewhere).
		if rel.Kind == versions.Node {
			if cur, cerr := d.env.GlobalNode(); use || cerr != nil || cur.Source != "download" {
				d.env.Cfg.Node.Version, nodeSwitched = v.Version, true
			}
		}
		if err == nil && (d.env.SyncServices() || rel.Kind == versions.Apache || nodeSwitched) {
			err = d.env.SaveConfig()
		}
		isDefault = d.env.Cfg.Service(string(rel.Kind)).Default == rel.Series
		d.cfgMu.Unlock()
		d.refreshShims()
	}
	d.jobs.update(j, func(j *Job) {
		j.Finished, j.Ended = true, time.Now()
		switch {
		case errors.Is(err, context.Canceled):
			j.Phase, j.Error = "cancelada", "instalación cancelada"
		case err != nil:
			j.Phase, j.Error = "error", err.Error()
		default:
			j.Phase = "instalado"
		}
	})
	switch {
	// The first version of a database becomes its default, which runs while Nido's services do.
	case err == nil && isServiceKind(rel.Kind) && isDefault:
		d.StartService(InstanceKey(rel.Kind, rel.Series))
	// Sites waiting for a web server start using it.
	case err == nil && (rel.Kind == versions.Nginx || rel.Kind == versions.FrankenPHP):
		if err := d.Rescan(); err != nil {
			d.setProblem("sitios", err)
		}
	// A downloaded Apache becomes the one that serves the sites.
	case err == nil && rel.Kind == versions.Apache:
		d.UseApache(v.Version)
	// Adminer opens at https://db.localhost right away.
	case err == nil && rel.Kind == versions.Adminer:
		if err := d.Rescan(); err != nil {
			d.setProblem("sitios", err)
		}
		d.setProblem("adminer", d.startAdminer(true))
	// The Vite servers move to the new global Node.
	case err == nil && rel.Kind == versions.Node:
		if nodeSwitched {
			d.restartNodeProcesses()
		}
		d.checkNodeNews(context.Background())
	}
	d.changed()
}

// Jobs lists the installations of the last minutes, oldest first.
func (d *Daemon) Jobs() []Job {
	d.jobs.mu.Lock()
	defer d.jobs.mu.Unlock()
	var out []Job
	for id, j := range d.jobs.jobs {
		if j.Finished && time.Since(j.Ended) > 15*time.Minute {
			delete(d.jobs.jobs, id)
			continue
		}
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Started.Before(out[k].Started) })
	return out
}

// CancelJob stops an installation that is still downloading.
func (d *Daemon) CancelJob(id string) error {
	d.jobs.mu.Lock()
	defer d.jobs.mu.Unlock()
	j, ok := d.jobs.jobs[id]
	if !ok {
		return fmt.Errorf("no hay ninguna instalación %s", id)
	}
	if !j.Finished {
		j.cancel()
	}
	return nil
}
