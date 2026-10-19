// Package meilisearch runs Meilisearch, the search engine behind Laravel Scout, in development mode
// (no master key needed; its web page lets you browse the indexes).
package meilisearch

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// Instance is one Meilisearch series ("meilisearch-1.55") with its data and port.
type Instance struct {
	Home    paths.Home
	Name    string
	Version versions.Version
	Port    int
}

func (i Instance) Key() string       { return i.Name }
func (i Instance) DataDir() string   { return i.Home.Data(i.Name) }
func (i Instance) LogPath() string   { return i.Home.Logs(i.Name + ".log") }
func (i Instance) Ports() []int      { return []int{i.Port} }
func (i Instance) Prepare() error    { return nil }
func (i Instance) Initialize() error { return os.MkdirAll(i.DataDir(), 0o755) }

// Spec describes Meilisearch. It only listens on 127.0.0.1.
func (i Instance) Spec() supervisor.Spec {
	return supervisor.Spec{
		Name: i.Name,
		Exe:  filepath.Join(i.Version.Path, "meilisearch.exe"),
		Args: []string{
			"--http-addr", fmt.Sprintf("127.0.0.1:%d", i.Port),
			"--db-path", filepath.Join(i.DataDir(), "data.ms"),
			"--dump-dir", filepath.Join(i.DataDir(), "dumps"),
			"--snapshot-dir", filepath.Join(i.DataDir(), "snapshots"),
			"--env", "development", "--no-analytics",
		},
		Dir:         i.DataDir(),
		LogPath:     i.LogPath(),
		Port:        i.Port,
		Restart:     true,
		StopTimeout: 10 * time.Second,
	}
}
