package app

import (
	"fmt"

	"github.com/nicotc/laraclon/internal/mailpit"
	"github.com/nicotc/laraclon/internal/meilisearch"
	"github.com/nicotc/laraclon/internal/mongodb"
	"github.com/nicotc/laraclon/internal/mysql"
	"github.com/nicotc/laraclon/internal/postgres"
	"github.com/nicotc/laraclon/internal/redis"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// webURL is the address of a service's own web page ("" when it has none).
func webURL(kind versions.Component, port int) string {
	switch kind {
	case versions.Mailpit:
		return fmt.Sprintf("http://127.0.0.1:%d/", secondPort(kind, port))
	case versions.Meilisearch:
		return fmt.Sprintf("http://127.0.0.1:%d/", port)
	}
	return ""
}

// runner runs one instance of a service; each kind of service implements it in its own package.
type runner interface {
	Key() string
	DataDir() string
	LogPath() string
	Ports() []int      // ports it listens on, checked before it starts
	Prepare() error    // writes its configuration file, if it has one
	Initialize() error // creates its data the first time
	Spec() supervisor.Spec
}

// externalData is implemented by runners that can open an existing data folder (Laragon's MySQL).
type externalData interface {
	InUseBy() (pid int, exe string, busy bool)
	Initialized() bool
	ExternalDir() string
}

// runnerFor builds the runnable instance of a series.
func (d *Daemon) runnerFor(kind versions.Component, series string) (runner, error) {
	key := InstanceKey(kind, series)
	cfg, ok := d.env.Cfg.Service(string(kind)).Instances[series]
	if !ok {
		return nil, fmt.Errorf("%s no está configurado", key)
	}
	if cfg.DataDir != "" && cfg.Version == "" {
		return nil, fmt.Errorf("%s usa la carpeta de datos %s pero no indica de qué versión es (version = \"x.y.z\" en clon.toml); clon no la abre con otra versión porque la actualizaría", key, cfg.DataDir)
	}
	v, err := d.env.InstanceVersion(kind, series)
	if err != nil {
		return nil, err
	}
	switch kind {
	case versions.MySQL, versions.MariaDB:
		return &mysql.Instance{Home: d.env.Home, Version: v, Port: cfg.Port, External: cfg.DataDir}, nil
	case versions.MongoDB:
		return &mongodb.Instance{Home: d.env.Home, Name: key, Version: v, Port: cfg.Port}, nil
	case versions.PostgreSQL:
		return &postgres.Instance{Home: d.env.Home, Name: key, Version: v, Port: cfg.Port}, nil
	case versions.Redis:
		return &redis.Instance{Home: d.env.Home, Name: key, Version: v, Port: cfg.Port}, nil
	case versions.Mailpit:
		return &mailpit.Instance{Home: d.env.Home, Name: key, Version: v, Port: cfg.Port, WebPort: secondPort(kind, cfg.Port)}, nil
	case versions.Meilisearch:
		return &meilisearch.Instance{Home: d.env.Home, Name: key, Version: v, Port: cfg.Port}, nil
	}
	return nil, fmt.Errorf("clon todavía no sabe ejecutar %s", kind.Name())
}
