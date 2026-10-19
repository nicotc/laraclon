package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/versions"
)

// serviceKind describes a kind of service clon runs as instances, one per series.
type serviceKind struct {
	kind   versions.Component
	port   int // standard port: the default instance gets it when free
	portTo int // last port tried for the other instances
	// autoDefault makes the first series of the kind its default even when it was imported (from
	// Laragon); otherwise only a version installed through clon becomes the default by itself, so
	// importing Laragon does not start services nobody asked for.
	autoDefault bool
}

// serviceKinds only lists the kinds clon knows how to run (see runnerFor).
var serviceKinds = []serviceKind{
	{versions.MySQL, 3306, 3399, true},
	{versions.MariaDB, 3306, 3399, true},
	{versions.PostgreSQL, 5432, 5499, false},
	{versions.MongoDB, 27017, 27099, false},
	{versions.Redis, 6379, 6399, false},
	{versions.Mailpit, 1025, 1099, false},
	{versions.Meilisearch, 7700, 7799, false},
}

// ServiceKinds are the services clon runs as instances, in the order the panel shows them.
var ServiceKinds = func() []versions.Component {
	var out []versions.Component
	for _, k := range serviceKinds {
		out = append(out, k.kind)
	}
	return out
}()

// DBStandardPort is the port MySQL and MariaDB clients assume.
const DBStandardPort = 3306

func kindInfo(c versions.Component) (serviceKind, bool) {
	for _, k := range serviceKinds {
		if k.kind == c {
			return k, true
		}
	}
	return serviceKind{}, false
}

func isServiceKind(c versions.Component) bool {
	_, ok := kindInfo(c)
	return ok
}

// StandardPort is the port clients of a kind of service assume (3306, 5432, 27017…).
func StandardPort(c versions.Component) int {
	k, _ := kindInfo(c)
	return k.port
}

// InstanceKey names an instance by kind and series, e.g. "mysql-8.4". It is also its service name,
// its data folder (data\mysql-8.4) and its log name.
func InstanceKey(kind versions.Component, series string) string { return string(kind) + "-" + series }

// ParseInstanceKey splits "mariadb-11.8" into its kind and series.
func ParseInstanceKey(key string) (versions.Component, string, error) {
	kind, series, ok := strings.Cut(strings.ToLower(strings.TrimSpace(key)), "-")
	c := versions.Component(kind)
	if !ok || !isServiceKind(c) || series == "" {
		return "", "", fmt.Errorf("servicio desconocido %q (por ejemplo mysql-8.4, postgresql-18 o redis-8.10)", key)
	}
	return c, series, nil
}

// InstanceRef is one configured instance with the version it runs.
type InstanceRef struct {
	Kind     versions.Component
	Series   string
	Instance config.ServiceInstance
	Version  versions.Version
}

func (r InstanceRef) Key() string { return InstanceKey(r.Kind, r.Series) }

// SeriesInstalled lists the series ("8.4", or "18" for PostgreSQL) of the installed versions of a
// kind, newest first.
func (e *Env) SeriesInstalled(kind versions.Component) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range e.Reg.List(kind) {
		s := versions.Series(kind, v.Version)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// InstanceVersion is the version an instance runs: the pinned one, or the newest of its series.
func (e *Env) InstanceVersion(kind versions.Component, series string) (versions.Version, error) {
	inst := e.Cfg.Service(string(kind)).Instances[series]
	want := series
	if inst.Version != "" {
		want = inst.Version
	}
	if v, ok := e.Reg.Find(kind, want); ok && versions.Series(kind, v.Version) == series {
		return v, nil
	}
	if inst.Version != "" {
		return versions.Version{}, fmt.Errorf("%s %s no está instalado", kind.Name(), inst.Version)
	}
	return versions.Version{}, fmt.Errorf("no hay ninguna versión %s de %s instalada", series, kind.Name())
}

// Instances lists the configured instances of a kind: the default first, then newest series first.
func (e *Env) Instances(kind versions.Component) []InstanceRef {
	svc := e.Cfg.Service(string(kind))
	var out []InstanceRef
	for series, inst := range svc.Instances {
		ref := InstanceRef{Kind: kind, Series: series, Instance: inst}
		ref.Version, _ = e.InstanceVersion(kind, series)
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Series == svc.Default) != (out[j].Series == svc.Default) {
			return out[i].Series == svc.Default
		}
		return versions.Compare(out[i].Series, out[j].Series) > 0
	})
	return out
}

// DefaultInstance is the default instance of a kind (ok is false when it has none).
func (e *Env) DefaultInstance(kind versions.Component) (InstanceRef, bool) {
	svc := e.Cfg.Service(string(kind))
	inst, ok := svc.Instances[svc.Default]
	if svc.Default == "" || !ok {
		return InstanceRef{}, false
	}
	v, err := e.InstanceVersion(kind, svc.Default)
	if err != nil {
		return InstanceRef{}, false
	}
	return InstanceRef{Kind: kind, Series: svc.Default, Instance: inst, Version: v}, true
}

// DefaultDB is the database the `mysql` shortcut and the status line use: the default MySQL, or
// else the default MariaDB.
func (e *Env) DefaultDB() (InstanceRef, bool) {
	for _, kind := range []versions.Component{versions.MySQL, versions.MariaDB} {
		if ref, ok := e.DefaultInstance(kind); ok {
			return ref, true
		}
	}
	return InstanceRef{}, false
}

// SyncServices brings the service settings up to date with the installed versions: it moves the
// [mysql] section of earlier versions into [services.mysql] and gives every installed series an
// instance with a stable port. It reports whether the settings changed (the caller saves them).
func (e *Env) SyncServices() bool {
	changed := e.migrateLegacyMySQL()
	if e.ensureInstances() {
		changed = true
	}
	return changed
}

// migrateLegacyMySQL moves the old [mysql] section into [services.mysql]. While no MySQL is
// installed the old section is kept, so its port is not lost.
func (e *Env) migrateLegacyMySQL() bool {
	old := e.Cfg.MySQL
	if old == nil {
		return false
	}
	want := old.Version
	if old.DataDirVersion != "" {
		want = old.DataDirVersion
	}
	series := ""
	if v, ok := e.Reg.Find(versions.MySQL, want); ok {
		series = versions.MajorMinor(v.Version)
	} else if strings.Count(want, ".") >= 1 {
		series = versions.MajorMinor(want)
	} else if v, ok := e.Reg.Find(versions.MySQL, ""); ok {
		series = versions.MajorMinor(v.Version)
	} else {
		return false
	}
	svc := e.Cfg.Service(string(versions.MySQL))
	inst := svc.Instances[series]
	if old.Port != 0 {
		inst.Port = old.Port
	}
	if inst.Port == 0 {
		inst.Port = DBStandardPort
	}
	if old.DataDir != "" {
		inst.DataDir = old.DataDir
		inst.Version = old.DataDirVersion
	}
	svc.Instances[series] = inst
	if old.Enabled && svc.Default == "" {
		svc.Default = series
	}
	e.Cfg.SetService(string(versions.MySQL), svc)
	e.Cfg.MySQL = nil
	return true
}

// ensureInstances creates the missing instances. The newest series of a kind without settings
// becomes its default (see serviceKind.autoDefault); the default gets the standard port when no
// other instance has it.
func (e *Env) ensureInstances() bool {
	changed := false
	for _, k := range serviceKinds {
		series := e.SeriesInstalled(k.kind)
		if len(series) == 0 {
			continue
		}
		svc := e.Cfg.Service(string(k.kind))
		switch {
		case svc.Default != "":
		case k.autoDefault && len(svc.Instances) == 0:
			svc.Default = series[0]
			changed = true
		case !k.autoDefault:
			// The newest series installed through clon (not imported) becomes the default.
			for _, s := range series {
				if v, _ := e.Reg.Find(k.kind, s); v.Source == "download" {
					svc.Default = s
					changed = true
					break
				}
			}
		}
		// The default goes first so it can claim the standard port.
		sort.SliceStable(series, func(i, j int) bool { return series[i] == svc.Default && series[j] != svc.Default })
		for _, s := range series {
			if _, ok := svc.Instances[s]; ok {
				continue
			}
			e.Cfg.SetService(string(k.kind), svc)
			svc.Instances[s] = config.ServiceInstance{Port: e.freePort(k.kind, s == svc.Default, "")}
			changed = true
		}
		e.Cfg.SetService(string(k.kind), svc)
	}
	return changed
}

// usedPorts returns the ports of every configured instance except skip, with the second port of
// the services that have one (Mailpit's web page).
func (e *Env) usedPorts(skip string) map[int]bool {
	used := map[int]bool{}
	for kind, svc := range e.Cfg.Services {
		for s, inst := range svc.Instances {
			if inst.Port != 0 && InstanceKey(versions.Component(kind), s) != skip {
				used[inst.Port] = true
				if p := secondPort(versions.Component(kind), inst.Port); p != 0 {
					used[p] = true
				}
			}
		}
	}
	return used
}

// secondPort is the extra port some services need next to their main one: Mailpit serves its web
// page on 8025 while it receives mail on 1025 (and 8026 for 1026…).
func secondPort(kind versions.Component, port int) int {
	if kind == versions.Mailpit {
		return port + 7000
	}
	return 0
}

// freePort picks a port no other instance is configured with. Only configured ports count, not
// programs running right now: a port Laragon holds today is free once Laragon is closed, and a
// clash is reported clearly when the instance starts.
func (e *Env) freePort(kind versions.Component, standard bool, skip string) int {
	k, _ := kindInfo(kind)
	used := e.usedPorts(skip)
	if standard && !used[k.port] {
		return k.port
	}
	for p := k.port + 1; p <= k.portTo; p++ {
		if !used[p] && !used[secondPort(kind, p)] && !ports.Reserved(p) {
			return p
		}
	}
	return 0
}

// CheckPort explains why an instance cannot use a port (another instance, clon's own ports or a
// range Windows reserves), or returns nil. Programs currently listening are checked at start.
func (e *Env) CheckPort(key string, port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("el puerto %d no es válido: usa uno entre 1024 y 65535", port)
	}
	kind, _, _ := ParseInstanceKey(key)
	wanted := []int{port}
	if p := secondPort(kind, port); p != 0 {
		wanted = append(wanted, p)
	}
	for k, svc := range e.Cfg.Services {
		for s, inst := range svc.Instances {
			other := InstanceKey(versions.Component(k), s)
			if other == key {
				continue
			}
			for _, w := range wanted {
				if inst.Port == w || secondPort(versions.Component(k), inst.Port) == w {
					return fmt.Errorf("el puerto %d ya es de %s", w, other)
				}
			}
		}
	}
	own := map[int]string{e.Cfg.Proxy.HTTPPort: "el proxy", e.Cfg.Proxy.HTTPSPort: "el proxy", apacheAdminPort: "Apache", nginxAdminPort: "Nginx", frankenAdminPort: "FrankenPHP", adminerPort: "Adminer"}
	for i := 0; i < e.Cfg.PHP.PoolSize; i++ {
		own[e.Cfg.PHP.BasePort+i] = "el grupo de PHP"
	}
	for _, w := range wanted {
		if who, ok := own[w]; ok {
			return fmt.Errorf("el puerto %d lo usa %s de clon", w, who)
		}
		if ports.Reserved(w) {
			return fmt.Errorf("el puerto %d está reservado por Windows (Hyper-V, WSL o Docker)", w)
		}
	}
	return nil
}
