// Package redis runs Redis instances (community builds for Windows), each with
// its own data folder and port, and stops them with redis-cli so the data is saved.
package redis

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// Instance is one Redis series ("redis-8.10") with its data and port.
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

// Initialize creates the data folder.
func (i Instance) Initialize() error { return os.MkdirAll(i.DataDir(), 0o755) }

// Prepare writes redis.conf inside the data folder. Redis runs from that folder and every path in
// the file is relative, because the Windows builds use Cygwin or MSYS2 paths internally.
func (i Instance) Prepare() error {
	if err := os.MkdirAll(i.DataDir(), 0o755); err != nil {
		return err
	}
	conf := fmt.Sprintf(`# Generado por Nido. No lo edites: se regenera al iniciar %s.
port %d
bind 127.0.0.1
protected-mode yes
dir ./
dbfilename dump.rdb
save 900 1
save 300 10
save 60 10000
appendonly no
logfile ""
`, i.Name, i.Port)
	return config.WriteFileAtomic(filepath.Join(i.DataDir(), "redis.conf"), []byte(conf))
}

// CLI is redis-cli.exe of a Redis version ("" when missing).
func CLI(v versions.Version) string {
	p := filepath.Join(v.Path, "redis-cli.exe")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// Spec describes redis-server. It only listens on 127.0.0.1.
func (i Instance) Spec() supervisor.Spec {
	spec := supervisor.Spec{
		Name:        i.Name,
		Exe:         filepath.Join(i.Version.Path, "redis-server.exe"),
		Args:        []string{"redis.conf"},
		Dir:         i.DataDir(),
		LogPath:     i.LogPath(),
		Port:        i.Port,
		Restart:     true,
		StopTimeout: 30 * time.Second,
	}
	if cli := CLI(i.Version); cli != "" {
		spec.Stop = func(int) error {
			// SHUTDOWN saves the data and closes the connection, so redis-cli's own result does not
			// tell anything: the supervisor waits for the server to exit instead.
			procutil.Output(20*time.Second, i.DataDir(), cli, "-p", strconv.Itoa(i.Port), "shutdown")
			return nil
		}
	}
	return spec
}
