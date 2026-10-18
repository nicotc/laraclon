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
