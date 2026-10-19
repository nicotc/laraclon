package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nicotc/laraclon/internal/versions"
)

func testDaemon(t *testing.T, e *Env) *Daemon {
	t.Helper()
	if err := e.Home.Ensure(); err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(e)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSetDefaultSwapsTheStandardPort(t *testing.T) {
	e := testEnv(t, mysqlV("8.4.11", "download"), mysqlV("5.7.44", "download"))
	e.SyncServices()
	d := testDaemon(t, e)
	if err := d.SetDefaultService("mysql-5.7"); err != nil {
		t.Fatal(err)
	}
	svc := e.Cfg.Service("mysql")
	if svc.Default != "5.7" || svc.Instances["5.7"].Port != 3306 || svc.Instances["8.4"].Port != 3307 {
		t.Fatalf("after the change: %+v", svc)
	}
	if db, ok := e.DefaultDB(); !ok || db.Version.Version != "5.7.44" || db.Instance.Port != 3306 {
		t.Fatalf("default database = %+v", db)
	}
}

func TestUninstallDeletesOnlyWhatNidoDownloaded(t *testing.T) {
	e := testEnv(t)
	if err := e.Home.Ensure(); err != nil {
		t.Fatal(err)
	}
	own := e.Home.Bin("mysql", "8.4.11")
	externalDir := filepath.Join(t.TempDir(), "mysql-8.0.30-winx64")
	data := e.Home.Data("mysql-8.4")
	for _, dir := range []string{own, externalDir, data} {
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644)
	}
	e.Reg.Add(versions.Version{Component: versions.MySQL, Version: "8.4.11", Path: own, Source: "download"})
	e.Reg.Add(versions.Version{Component: versions.MySQL, Version: "8.0.30", Path: externalDir, Source: "import"})
	e.SyncServices()
	d := testDaemon(t, e)

	if err := d.UninstallService("mysql-8.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(externalDir); err != nil {
		t.Fatal("binaries Nido did not download must never be deleted")
	}
	if err := d.UninstallService("mysql-8.4"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Fatal("a downloaded version must be deleted")
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatal("the data folder must be kept")
	}
	if len(e.Reg.List(versions.MySQL)) != 0 || len(e.Cfg.Service("mysql").Instances) != 0 {
		t.Fatal("registry and settings must forget both")
	}
}

func TestServiceSettingsRefusesAPortOfAnotherInstance(t *testing.T) {
	e := testEnv(t, mysqlV("8.4.11", "download"), mariaV("11.8.9"))
	e.SyncServices()
	d := testDaemon(t, e)
	taken := e.Cfg.Service("mysql").Instances["8.4"].Port
	if err := d.ServiceSettings("mariadb-11.8", &taken, nil); err == nil {
		t.Fatal("two instances must never share a port")
	}
	free, on := 3333, true
	if err := d.ServiceSettings("mariadb-11.8", &free, &on); err != nil {
		t.Fatal(err)
	}
	if inst := e.Cfg.Service("mariadb").Instances["11.8"]; inst.Port != 3333 || !inst.Autostart {
		t.Fatalf("settings not saved: %+v", inst)
	}
}
