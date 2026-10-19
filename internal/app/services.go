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
	"github.com/nicotc/laraclon/internal/mysql"
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
}

// dbGroups describes every kind of database for the status.
func (d *Daemon) dbGroups(problems map[string]string) []DBGroup {
	byName := map[string]supervisorState{}
	for _, s := range d.sup.Statuses() {
		byName[s.Name] = supervisorState{state: string(s.State), pid: s.PID, err: s.LastError}
	}
	var out []DBGroup
	for _, kind := range DBKinds {
		svc := d.env.Cfg.Service(string(kind))
		g := DBGroup{Kind: string(kind), Name: kind.Name(), Default: svc.Default}
		for _, ref := range d.env.DBInstances(kind) {
			key := ref.Key()
			st := InstanceStatus{
				Key: key, Series: ref.Series, Version: ref.Version.Version, Source: ref.Version.Source,
				Port: ref.Instance.Port, Autostart: ref.Instance.Autostart || ref.Series == svc.Default,
				Default: ref.Series == svc.Default, State: "detenido", External: ref.Instance.DataDir != "",
				DataDir: ref.Instance.DataDir,
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

// dbInstance builds the runnable instance of a series.
func (d *Daemon) dbInstance(kind versions.Component, series string) (*mysql.Instance, error) {
	key := InstanceKey(kind, series)
	cfg, ok := d.env.Cfg.Service(string(kind)).Instances[series]
	if !ok {
		return nil, fmt.Errorf("%s no está configurado", key)
	}
	if cfg.DataDir != "" && cfg.Version == "" {
		return nil, fmt.Errorf("%s usa la carpeta de datos %s pero no indica de qué versión es (version = \"x.y.z\" en clon.toml); clon no la abre con otra versión porque la actualizaría", key, cfg.DataDir)
	}
	v, err := d.env.InstanceVersion(kind, series)
	if err != nil {
		return nil, err
	}
	return &mysql.Instance{Home: d.env.Home, Version: v, Port: cfg.Port, External: cfg.DataDir}, nil
}

// startDB starts one instance: writes my.ini, creates the data folder the first time and launches
// the server under the supervisor.
func (d *Daemon) startDB(kind versions.Component, series string) error {
	inst, err := d.dbInstance(kind, series)
	if err != nil {
		return err
	}
	if d.sup.PID(inst.ServiceName()) != 0 {
		return nil
	}
	if pid, exe, busy := inst.InUseBy(); busy {
		return fmt.Errorf("otro servidor ya usa la carpeta de datos %s: %s (PID %d). Detenlo (por ejemplo, cierra Laragon) antes de iniciar el de clon", inst.DataDir(), exe, pid)
	}
	if inst.External != "" && !inst.Initialized() {
		return fmt.Errorf("%s no contiene datos de MySQL", inst.DataDir())
	}
	if err := ports.Check(inst.Port); err != nil {
		return fmt.Errorf("%s no puede usar su puerto: %w. Cámbialo en el panel (Servicios) o con 'clon service port %s <puerto>'", inst.Key(), err, inst.Key())
	}
	if err := inst.WriteIni(); err != nil {
		return err
	}
	if err := inst.Initialize(); err != nil {
		return err
	}
	return d.sup.Start(inst.Spec())
}

// startDatabases starts the default instance of each kind and the ones marked to start with
// «Iniciar todo». Each instance reports its own problem.
func (d *Daemon) startDatabases() {
	for _, kind := range DBKinds {
		svc := d.env.Cfg.Service(string(kind))
		for _, ref := range d.env.DBInstances(kind) {
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
		if err := d.env.CheckDBPort(key, *port); err != nil {
			return err
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
	if oldInst, ok := svc.Instances[old]; ok && oldInst.Port == DBStandardPort {
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
	// The default always runs while clon's services do.
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
// deleted; versions that belong to Laragon are only forgotten. The data folder is always kept.
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
		if versions.MajorMinor(v.Version) != series {
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
			if removed.Port == DBStandardPort && d.sup.PID(InstanceKey(kind, rest[0])) == 0 {
				next.Port = DBStandardPort
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
	inst, err := d.dbInstance(kind, series)
	if err != nil {
		return err
	}
	target := inst.DataDir()
	if what == "log" {
		target = inst.LogPath()
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
	if v, ok := d.env.Reg.Find(kind, rel.Version); ok && v.Version == rel.Version {
		return Job{}, fmt.Errorf("%s ya está instalado", v.Label())
	}
	d.jobs.mu.Lock()
	defer d.jobs.mu.Unlock()
	for _, j := range d.jobs.jobs {
		if !j.Finished && j.Kind == string(kind) && j.Series == rel.Series {
			return *j, nil
		}
	}
	id := make([]byte, 6)
	rand.Read(id)
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{ID: hex.EncodeToString(id), Kind: string(kind), Series: rel.Series, Version: rel.Version,
		Phase: "preparando", Total: rel.Size, Started: time.Now(), cancel: cancel}
	d.jobs.jobs[j.ID] = j
	go d.runInstall(ctx, j, rel)
	return *j, nil
}

func (d *Daemon) runInstall(ctx context.Context, j *Job, rel install.Release) {
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
	isDefault := false
	if err == nil {
		d.cfgMu.Lock()
		d.env.Reg.Add(v)
		err = d.env.SaveRegistry()
		if err == nil && d.env.SyncServices() {
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
	// The first version of a database becomes its default, which runs while clon's services do.
	case err == nil && isDBKind(rel.Kind) && isDefault:
		d.StartService(InstanceKey(rel.Kind, rel.Series))
	// Sites waiting for a web server start using it.
	case err == nil && (rel.Kind == versions.Nginx || rel.Kind == versions.FrankenPHP):
		if err := d.Rescan(); err != nil {
			d.setProblem("sitios", err)
		}
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
