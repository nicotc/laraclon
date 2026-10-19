// Package postgres runs PostgreSQL instances, each with its own data folder and port. The server runs
// without administrator rights (PostgreSQL refuses them) and stops cleanly with pg_ctl.
package postgres

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// Instance is one PostgreSQL major version ("postgresql-18") with its data and port.
type Instance struct {
	Home    paths.Home
	Name    string
	Version versions.Version
	Port    int
}

func (i Instance) Key() string     { return i.Name }
func (i Instance) DataDir() string { return i.Home.Data(i.Name) }
func (i Instance) LogPath() string { return i.Home.Logs(i.Name + ".log") }
func (i Instance) Ports() []int    { return []int{i.Port} }
func (i Instance) Prepare() error  { return nil }

func (i Instance) bin(name string) string { return filepath.Join(i.Version.Path, "bin", name) }

// Initialized reports whether the data folder already holds a cluster.
func (i Instance) Initialized() bool {
	_, err := os.Stat(filepath.Join(i.DataDir(), "PG_VERSION"))
	return err == nil
}

// Initialize creates the cluster: user postgres without password (only reachable on 127.0.0.1),
// UTF-8. initdb drops administrator rights by itself.
func (i Instance) Initialize() error {
	if i.Initialized() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(i.DataDir()), 0o755); err != nil {
		return err
	}
	out, err := procutil.Output(5*time.Minute, i.Version.Path, i.bin("initdb.exe"),
		"-D", i.DataDir(), "-U", "postgres", "-A", "trust", "-E", "UTF8", "--locale=C")
	if err != nil {
		os.RemoveAll(i.DataDir())
		return fmt.Errorf("no se pudo inicializar %s: %s", i.Name, procutil.LastLines(out, 6))
	}
	return nil
}

// Psql is the command-line client of a PostgreSQL version ("" when missing).
func Psql(v versions.Version) string {
	p := filepath.Join(v.Path, "bin", "psql.exe")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// Spec describes the server. It only listens on 127.0.0.1.
func (i Instance) Spec() supervisor.Spec {
	return supervisor.Spec{
		Name:         i.Name,
		Exe:          i.bin("postgres.exe"),
		Args:         []string{"-D", i.DataDir(), "-p", strconv.Itoa(i.Port), "-h", "127.0.0.1"},
		Dir:          i.Version.Path,
		LogPath:      i.LogPath(),
		Port:         i.Port,
		Restart:      true,
		Unprivileged: true,
		Stop: func(int) error {
			_, err := procutil.Output(45*time.Second, i.Version.Path, i.bin("pg_ctl.exe"), "stop", "-D", i.DataDir(), "-m", "fast", "-w", "-t", "40")
			return err
		},
		StopTimeout: 50 * time.Second,
	}
}
