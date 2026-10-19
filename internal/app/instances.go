package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/ports"
	"github.com/nicotc/laraclon/internal/versions"
)

// DBKinds are the databases clon runs as services, in the order the panel shows them.
var DBKinds = []versions.Component{versions.MySQL, versions.MariaDB}

// DBStandardPort is the port MySQL and MariaDB clients assume. The default instance gets it; the
// other instances get the next free ports up to dbPortTo.
const (
	DBStandardPort = 3306
	dbPortTo       = 3399
)

func isDBKind(c versions.Component) bool {
	for _, k := range DBKinds {
		if k == c {
			return true
		}
	}
	return false
}

// InstanceKey names an instance by kind and series, e.g. "mysql-8.4". It is also its service name,
// its data folder (data\mysql-8.4) and its log name.
func InstanceKey(kind versions.Component, series string) string { return string(kind) + "-" + series }

// ParseInstanceKey splits "mariadb-11.8" into its kind and series.
func ParseInstanceKey(key string) (versions.Component, string, error) {
	kind, series, ok := strings.Cut(strings.ToLower(strings.TrimSpace(key)), "-")
	c := versions.Component(kind)
	if !ok || !isDBKind(c) || series == "" {
		return "", "", fmt.Errorf("servicio desconocido %q (por ejemplo mysql-8.4 o mariadb-11.8)", key)
	}
	return c, series, nil
}

// DBRef is one configured instance with the version it runs.
type DBRef struct {
	Kind     versions.Component
	Series   string
	Instance config.ServiceInstance
	Version  versions.Version
}

func (r DBRef) Key() string { return InstanceKey(r.Kind, r.Series) }

// SeriesInstalled lists the series ("8.4") of the installed versions of a kind, newest first.
func (e *Env) SeriesInstalled(kind versions.Component) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range e.Reg.List(kind) {
		s := versions.MajorMinor(v.Version)
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
	if v, ok := e.Reg.Find(kind, want); ok && versions.MajorMinor(v.Version) == series {
		return v, nil
	}
	if inst.Version != "" {
		return versions.Version{}, fmt.Errorf("%s %s no está instalado", kind.Name(), inst.Version)
	}
	return versions.Version{}, fmt.Errorf("no hay ninguna versión %s de %s instalada", series, kind.Name())
}

// DBInstances lists the configured instances of a kind: the default first, then newest series first.
func (e *Env) DBInstances(kind versions.Component) []DBRef {
	svc := e.Cfg.Service(string(kind))
	var out []DBRef
	for series, inst := range svc.Instances {
		ref := DBRef{Kind: kind, Series: series, Instance: inst}
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

// DefaultDB is the instance the `mysql` shortcut and the status line use: the default MySQL, or
// else the default MariaDB.
func (e *Env) DefaultDB() (DBRef, bool) {
	for _, kind := range DBKinds {
		svc := e.Cfg.Service(string(kind))
		inst, ok := svc.Instances[svc.Default]
		if svc.Default == "" || !ok {
			continue
		}
		v, err := e.InstanceVersion(kind, svc.Default)
		if err != nil {
			continue
		}
		return DBRef{Kind: kind, Series: svc.Default, Instance: inst, Version: v}, true
	}
	return DBRef{}, false
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
// becomes its default; the default gets the standard port when no other instance has it.
func (e *Env) ensureInstances() bool {
	changed := false
	for _, kind := range DBKinds {
		series := e.SeriesInstalled(kind)
		if len(series) == 0 {
			continue
		}
		svc := e.Cfg.Service(string(kind))
		if svc.Default == "" && len(svc.Instances) == 0 {
			svc.Default = series[0]
			changed = true
		}
		// The default goes first so it can claim the standard port.
		sort.SliceStable(series, func(i, j int) bool { return series[i] == svc.Default && series[j] != svc.Default })
		for _, s := range series {
			if _, ok := svc.Instances[s]; ok {
				continue
			}
			e.Cfg.SetService(string(kind), svc)
			svc.Instances[s] = config.ServiceInstance{Port: e.freeDBPort(s == svc.Default, "")}
			changed = true
		}
		e.Cfg.SetService(string(kind), svc)
	}
	return changed
}

// usedDBPorts returns the ports of every configured instance except skip.
func (e *Env) usedDBPorts(skip string) map[int]bool {
	used := map[int]bool{}
	for kind, svc := range e.Cfg.Services {
		for s, inst := range svc.Instances {
			if inst.Port != 0 && InstanceKey(versions.Component(kind), s) != skip {
				used[inst.Port] = true
			}
		}
	}
	return used
}

// freeDBPort picks a port no other instance is configured with. Only configured ports count, not
// programs running right now: a port Laragon holds today is free once Laragon is closed, and a
// clash is reported clearly when the instance starts.
func (e *Env) freeDBPort(standard bool, skip string) int {
	used := e.usedDBPorts(skip)
	if standard && !used[DBStandardPort] {
		return DBStandardPort
	}
	for p := DBStandardPort + 1; p <= dbPortTo; p++ {
		if !used[p] && !ports.Reserved(p) {
			return p
		}
	}
	return 0
}

// CheckDBPort explains why an instance cannot use a port (another instance, clon's own ports or a
// range Windows reserves), or returns nil. Programs currently listening are checked at start.
func (e *Env) CheckDBPort(key string, port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("el puerto %d no es válido: usa uno entre 1024 y 65535", port)
	}
	for kind, svc := range e.Cfg.Services {
		for s, inst := range svc.Instances {
			if other := InstanceKey(versions.Component(kind), s); other != key && inst.Port == port {
				return fmt.Errorf("el puerto %d ya es de %s", port, other)
			}
		}
	}
	own := map[int]string{e.Cfg.Proxy.HTTPPort: "el proxy", e.Cfg.Proxy.HTTPSPort: "el proxy", apacheAdminPort: "Apache"}
	for i := 0; i < e.Cfg.PHP.PoolSize; i++ {
		own[e.Cfg.PHP.BasePort+i] = "el grupo de PHP"
	}
	if who, ok := own[port]; ok {
		return fmt.Errorf("el puerto %d lo usa %s de clon", port, who)
	}
	if port >= sitePortFrom && port <= sitePortTo {
		return fmt.Errorf("los puertos %d-%d son para los sitios", sitePortFrom, sitePortTo)
	}
	if ports.Reserved(port) {
		return fmt.Errorf("el puerto %d está reservado por Windows (Hyper-V, WSL o Docker)", port)
	}
	return nil
}
