package mysql

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestExternalDataFolderIsNeverInitialisedOrRemoved(t *testing.T) {
	ext := t.TempDir()
	marker := filepath.Join(ext, "keep-me.txt")
	os.WriteFile(marker, []byte("datos del usuario"), 0o644)
	inst := Instance{
		Home:     paths.Home{Root: t.TempDir()},
		Version:  versions.Version{Component: versions.MySQL, Version: "8.0.30", Path: `C:\no-existe`},
		Port:     3399,
		External: ext,
	}
	if err := inst.Initialize(); err == nil {
		t.Fatal("an external folder without MySQL data must be refused")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the external folder must be left untouched")
	}
}

func TestInUseByFindsALiveMySQLThroughItsPidFile(t *testing.T) {
	ext := t.TempDir()
	inst := Instance{Home: paths.Home{Root: t.TempDir()}, External: ext}
	// A pid file that points to this test process: alive, but not mysqld.exe.
	os.WriteFile(filepath.Join(ext, "host.pid"), []byte(fmt.Sprint(os.Getpid())), 0o644)
	if _, _, busy := inst.InUseBy(); busy {
		t.Fatal("a process that is not mysqld.exe must not count as the owner")
	}
	os.WriteFile(filepath.Join(ext, "host.pid"), []byte("999999"), 0o644)
	if _, _, busy := inst.InUseBy(); busy {
		t.Fatal("a stale pid file must not block")
	}
}

func TestServerMessageKeepsWhatTheServerSays(t *testing.T) {
	for line, want := range map[string]string{
		"2026-10-19T20:45:53.314865Z 0 [ERROR] [MY-000067] [Server] unknown variable 'variable_inventada=1'.":    "unknown variable 'variable_inventada=1'.",
		"2026-10-19T20:46:11.941713Z 0 [ERROR] unknown variable 'variable_inventada=1'":                          "unknown variable 'variable_inventada=1'",
		`2026-10-19 16:46:08 0 [ERROR] E:\nido\bin\mariadb\11.8.9\bin\mariadbd.exe: unknown variable 'x=1'`:      "unknown variable 'x=1'",
		"2026-10-19 16:46:09 0 [Warning] option 'innodb-buffer-pool-size': unsigned value 0 adjusted to 2097152": "option 'innodb-buffer-pool-size': unsigned value 0 adjusted to 2097152",
	} {
		if got := serverMessage(line); got != want {
			t.Errorf("serverMessage(%q) = %q, quiero %q", line, got, want)
		}
	}
}
