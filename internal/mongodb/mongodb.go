// Package mongodb runs MongoDB instances, each with its own data folder and port, and stops them
// cleanly through mongosh (installed next to the server).
package mongodb

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// Instance is one MongoDB series ("mongodb-9.0") with its data and port.
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

// Initialize creates the data folder; mongod fills it on its first start.
func (i Instance) Initialize() error { return os.MkdirAll(i.DataDir(), 0o755) }

// Shell is the mongosh installed with a MongoDB version ("" when there is none).
func Shell(v versions.Version) string {
	p := filepath.Join(v.Path, "mongosh", "bin", "mongosh.exe")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// Spec describes mongod. It only listens on 127.0.0.1.
func (i Instance) Spec() supervisor.Spec {
	spec := supervisor.Spec{
		Name:        i.Name,
		Exe:         filepath.Join(i.Version.Path, "bin", "mongod.exe"),
		Args:        []string{"--dbpath", i.DataDir(), "--port", strconv.Itoa(i.Port), "--bind_ip", "127.0.0.1"},
		Dir:         i.Version.Path,
		LogPath:     i.LogPath(),
		Port:        i.Port,
		Restart:     true,
		StopTimeout: 60 * time.Second,
	}
	if shell := Shell(i.Version); shell != "" {
		spec.Stop = func(int) error {
			// The server drops the connection while it shuts down, so mongosh's own result does not
			// tell anything: the supervisor waits for mongod to exit instead.
			procutil.Output(30*time.Second, i.Version.Path, shell, "--quiet", "--port", strconv.Itoa(i.Port),
				"--eval", "db.getSiblingDB('admin').shutdownServer()")
			return nil
		}
	}
	return spec
}
