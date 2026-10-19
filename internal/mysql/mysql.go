// Package mysql runs MySQL (and MariaDB) instances with their own data folder and port.
package mysql

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// Instance is one MySQL version with its data and port.
type Instance struct {
	Home     paths.Home
	Version  versions.Version
	Port     int
	External string // existing data folder to use instead of data\<key>
}

// Key names the instance by component and major version, e.g. "mysql-8.0".
func (i Instance) Key() string {
	return string(i.Version.Component) + "-" + versions.MajorMinor(i.Version.Version)
}

func (i Instance) ServiceName() string { return i.Key() }
func (i Instance) IniPath() string     { return i.Home.Generated(i.Key(), "my.ini") }

func (i Instance) DataDir() string {
	if i.External != "" {
		return i.External
	}
	return i.Home.Data(i.Key())
}

// InUseBy reports a running MySQL that owns the data folder, found through the <host>.pid file
// MySQL keeps there while it runs. Starting a second server on the same files is refused early.
func (i Instance) InUseBy() (pid int, exe string, busy bool) {
	matches, _ := filepath.Glob(filepath.Join(i.DataDir(), "*.pid"))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var p int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &p); err != nil || p == 0 {
			continue
		}
		if path, alive := processImage(p); alive && (strings.EqualFold(filepath.Base(path), "mysqld.exe") || strings.EqualFold(filepath.Base(path), "mariadbd.exe")) {
			return p, path, true
		}
	}
	return 0, "", false
}
func (i Instance) bin(name string) string {
	return filepath.Join(i.Version.Path, "bin", name)
}

// firstBin returns the first program of names that exists in bin\ (MariaDB renamed its programs:
// mysqld.exe became mariadbd.exe and the old names are not always shipped), or the first name.
func firstBin(v versions.Version, names ...string) string {
	for _, n := range names {
		p := filepath.Join(v.Path, "bin", n)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(v.Path, "bin", names[0])
}

func (i Instance) serverExe() string {
	if i.Version.Component == versions.MariaDB {
		return firstBin(i.Version, "mariadbd.exe", "mysqld.exe")
	}
	return firstBin(i.Version, "mysqld.exe")
}

func (i Instance) adminExe() string {
	if i.Version.Component == versions.MariaDB {
		return firstBin(i.Version, "mariadb-admin.exe", "mysqladmin.exe")
	}
	return firstBin(i.Version, "mysqladmin.exe")
}

// ClientExe is the command-line client of a MySQL or MariaDB version ("" when it has none).
func ClientExe(v versions.Version) string {
	names := []string{"mysql.exe"}
	if v.Component == versions.MariaDB {
		names = []string{"mariadb.exe", "mysql.exe"}
	}
	p := firstBin(v, names...)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// LogPath is the log of the instance's server.
func (i Instance) LogPath() string { return i.Home.Logs(i.Key() + ".log") }

// Ports are the ports the server listens on.
func (i Instance) Ports() []int { return []int{i.Port} }

// Prepare writes my.ini.
func (i Instance) Prepare() error { return i.WriteIni() }

// ExternalDir is the existing data folder the instance opens ("" for clon's own).
func (i Instance) ExternalDir() string { return i.External }

var iniTemplate = template.Must(template.New("my.ini").Parse(`# Generado por clon. Pon tus cambios en {{.Overrides}}
[client]
host=127.0.0.1
port={{.Port}}
user=root

[mysqld]
basedir="{{.Base}}"
datadir="{{.Data}}"
port={{.Port}}
bind-address=127.0.0.1
character-set-server=utf8mb4
max_allowed_packet=256M
# Same server mode as Laragon (no ONLY_FULL_GROUP_BY), so existing queries behave the same.
sql_mode="STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION"
secure-file-priv=""
explicit_defaults_for_timestamp=1
loose-mysqlx=OFF
{{.Extra}}`))

// WriteIni renders my.ini. Settings in config\<key>.ini are appended under [mysqld].
func (i Instance) WriteIni() error {
	extra := ""
	isMySQL := i.Version.Component == versions.MySQL
	switch {
	case isMySQL && versions.Compare(i.Version.Version, "8.4") >= 0:
		// MySQL 8.4 ships mysql_native_password disabled; enabling it keeps old PHP clients working.
		extra += "loose-mysql_native_password=ON\n"
	case isMySQL && versions.Compare(i.Version.Version, "8.0") >= 0:
		extra += "default_authentication_plugin=mysql_native_password\n"
	}
	overrides := i.Home.Config(i.Key() + ".ini")
	if data, err := os.ReadFile(overrides); err == nil {
		extra += "\n# ---- " + overrides + " ----\n" + string(data) + "\n"
	}
	var b strings.Builder
	err := iniTemplate.Execute(&b, map[string]any{
		"Overrides": overrides,
		"Port":      i.Port,
		"Base":      paths.Slash(i.Version.Path),
		"Data":      paths.Slash(i.DataDir()),
		"Extra":     extra,
	})
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(i.IniPath(), []byte(b.String()))
}

// Initialized reports whether the data folder already holds the system database.
func (i Instance) Initialized() bool {
	_, err := os.Stat(filepath.Join(i.DataDir(), "mysql"))
	return err == nil
}

// Initialize creates the data folder with a root user without password (only reachable on 127.0.0.1).
// An external data folder (Laragon's) is never initialised, modified or removed here.
func (i Instance) Initialize() error {
	if i.Initialized() {
		return nil
	}
	if i.External != "" {
		return fmt.Errorf("%s no contiene datos de MySQL", i.DataDir())
	}
	if err := os.MkdirAll(filepath.Dir(i.DataDir()), 0o755); err != nil {
		return err
	}
	var out string
	var err error
	if i.Version.Component == versions.MariaDB {
		tool := i.bin("mariadb-install-db.exe")
		if _, statErr := os.Stat(tool); statErr != nil {
			tool = i.bin("mysql_install_db.exe")
		}
		out, err = procutil.Output(5*time.Minute, i.Version.Path, tool, "--datadir="+i.DataDir(), fmt.Sprintf("--port=%d", i.Port))
	} else {
		out, err = procutil.Output(5*time.Minute, i.Version.Path, i.serverExe(), "--defaults-file="+i.IniPath(), "--initialize-insecure", "--console")
	}
	if err != nil {
		os.RemoveAll(i.DataDir())
		return fmt.Errorf("no se pudo inicializar %s: %s", i.Key(), procutil.LastLines(out, 6))
	}
	return nil
}

// Spec describes the server process. Stopping asks mysqladmin for a clean shutdown first.
func (i Instance) Spec() supervisor.Spec {
	return supervisor.Spec{
		Name:    i.ServiceName(),
		Exe:     i.serverExe(),
		Args:    []string{"--defaults-file=" + i.IniPath(), "--console"},
		Dir:     i.Version.Path,
		LogPath: i.LogPath(),
		Port:    i.Port,
		Restart: true,
		Stop: func(int) error {
			_, err := procutil.Output(30*time.Second, i.Version.Path, i.adminExe(), "--defaults-file="+i.IniPath(), "shutdown")
			return err
		},
		StopTimeout: 45 * time.Second,
	}
}
