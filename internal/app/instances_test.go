package app

import (
	"testing"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

func testEnv(t *testing.T, installed ...versions.Version) *Env {
	t.Helper()
	home := paths.Home{Root: t.TempDir()}
	reg, err := versions.Load(home.State("versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range installed {
		reg.Add(v)
	}
	return &Env{Home: home, Cfg: config.Defaults(), Reg: reg}
}

func mysqlV(v, source string) versions.Version {
	return versions.Version{Component: versions.MySQL, Version: v, Path: `C:\x\mysql-` + v, Source: source}
}

func mariaV(v string) versions.Version {
	return versions.Version{Component: versions.MariaDB, Version: v, Path: `C:\x\mariadb-` + v, Source: "download"}
}

func TestLegacyMySQLSectionBecomesTheDefaultInstance(t *testing.T) {
	e := testEnv(t, mysqlV("8.0.30", "laragon"), mysqlV("5.7.42", "laragon"))
	e.Cfg.MySQL = &config.LegacyMySQL{Enabled: true, Version: "8.0.30", Port: 3307,
		DataDir: `C:\laragon\data\mysql-8`, DataDirVersion: "8.0.30"}
	if !e.SyncServices() {
		t.Fatal("the old section must be migrated")
	}
	if e.Cfg.MySQL != nil {
		t.Fatal("the old section must be dropped once migrated")
	}
	svc := e.Cfg.Service("mysql")
	if svc.Default != "8.0" {
		t.Fatalf("default = %q, want 8.0", svc.Default)
	}
	got := svc.Instances["8.0"]
	if got.Port != 3307 || got.DataDir != `C:\laragon\data\mysql-8` || got.Version != "8.0.30" {
		t.Fatalf("8.0 instance = %+v", got)
	}
	// 5.7 gets the next free port, never the standard one (it is not the default).
	if p := svc.Instances["5.7"].Port; p != 3308 {
		t.Fatalf("5.7 port = %d, want 3308", p)
	}
	if e.SyncServices() {
		t.Fatal("a second sync must change nothing")
	}
}

func TestLegacySectionWaitsUntilAMySQLIsInstalled(t *testing.T) {
	e := testEnv(t)
	e.Cfg.MySQL = &config.LegacyMySQL{Enabled: true, Port: 3310}
	if e.SyncServices() || e.Cfg.MySQL == nil {
		t.Fatal("without any MySQL the old section (and its port) must be kept")
	}
	e.Reg.Add(mysqlV("8.4.11", "download"))
	e.SyncServices()
	if p := e.Cfg.Service("mysql").Instances["8.4"].Port; p != 3310 {
		t.Fatalf("port = %d, want the old 3310", p)
	}
}

func TestFirstSeriesOfEachKindIsItsDefaultAndPortsNeverClash(t *testing.T) {
	e := testEnv(t, mysqlV("8.4.11", "download"), mysqlV("8.0.46", "download"), mariaV("11.8.9"))
	e.SyncServices()
	my, maria := e.Cfg.Service("mysql"), e.Cfg.Service("mariadb")
	if my.Default != "8.4" || maria.Default != "11.8" {
		t.Fatalf("defaults = %q / %q", my.Default, maria.Default)
	}
	if my.Instances["8.4"].Port != 3306 {
		t.Fatalf("the default MySQL must get 3306, got %d", my.Instances["8.4"].Port)
	}
	seen := map[int]string{}
	for kind, svc := range e.Cfg.Services {
		for s, inst := range svc.Instances {
			key := InstanceKey(versions.Component(kind), s)
			if other, dup := seen[inst.Port]; dup {
				t.Fatalf("%s and %s share port %d", key, other, inst.Port)
			}
			seen[inst.Port] = key
		}
	}
	// A newer patch of an installed series is used by the same instance.
	e.Reg.Add(mysqlV("8.4.12", "download"))
	if e.SyncServices() {
		t.Fatal("a new patch release must not create another instance")
	}
	if v, _ := e.InstanceVersion(versions.MySQL, "8.4"); v.Version != "8.4.12" {
		t.Fatalf("instance runs %s, want 8.4.12", v.Version)
	}
}

func TestPinnedVersionIsNeverReplacedByANewerPatch(t *testing.T) {
	e := testEnv(t, mysqlV("8.0.30", "laragon"), mysqlV("8.0.46", "download"))
	e.Cfg.SetService("mysql", config.Service{Default: "8.0", Instances: map[string]config.ServiceInstance{
		"8.0": {Port: 3306, Version: "8.0.30", DataDir: `C:\laragon\data\mysql-8`},
	}})
	if v, _ := e.InstanceVersion(versions.MySQL, "8.0"); v.Version != "8.0.30" {
		t.Fatalf("pinned instance runs %s, want 8.0.30", v.Version)
	}
}

func TestCheckPort(t *testing.T) {
	e := testEnv(t, mysqlV("8.4.11", "download"), mysqlV("5.7.44", "download"))
	e.SyncServices()
	if err := e.CheckPort("mysql-5.7", 3306); err == nil {
		t.Fatal("3306 belongs to mysql-8.4")
	}
	if err := e.CheckPort("mysql-8.4", 3306); err != nil {
		t.Fatalf("an instance may keep its own port: %v", err)
	}
	for _, p := range []int{80, 443, 9100, 20000, 30000, 40000, 99999} {
		if err := e.CheckPort("mysql-5.7", p); err == nil {
			t.Fatalf("port %d must be refused", p)
		}
	}
	if err := e.CheckPort("mysql-5.7", 3320); err != nil {
		t.Fatalf("3320 should be fine: %v", err)
	}
}

func TestParseInstanceKey(t *testing.T) {
	kind, series, err := ParseInstanceKey("MariaDB-11.8")
	if err != nil || kind != versions.MariaDB || series != "11.8" {
		t.Fatalf("got %s %s %v", kind, series, err)
	}
	for _, bad := range []string{"php-8.4", "mysql", "mysql-", "8.4"} {
		if _, _, err := ParseInstanceKey(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}
