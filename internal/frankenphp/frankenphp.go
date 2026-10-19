// Package frankenphp writes a Caddyfile (one internal port per site that chose FrankenPHP) and
// controls FrankenPHP, which serves PHP with the PHP built into it rather than the global one.
package frankenphp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/template"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// ServiceName is the supervisor name of the FrankenPHP process.
const ServiceName = "frankenphp"

// Site is what FrankenPHP needs to know about one site.
type Site struct {
	Host       string
	DocRoot    string
	Port       int
	HTTPS      bool
	PublicPort int // the port the browser uses, handed to PHP as SERVER_PORT
}

// Options describe one generated configuration.
type Options struct {
	Home      paths.Home
	Version   versions.Version
	AdminPort int    // Caddy's admin API: health check, reload and stop
	IniDir    string // folder with the php.ini Nido generates for FrankenPHP's PHP (PHPRC)
	Sites     []Site
}

var caddyTemplate = template.Must(template.New("Caddyfile").Funcs(template.FuncMap{"q": strconv.Quote}).Parse(`# Generado por Nido el {{.Now}}. No lo edites: se regenera al cambiar sitios o versiones.
{
	admin 127.0.0.1:{{.AdminPort}}
	persist_config off
	auto_https off
	frankenphp
}
{{range .Sites}}
# {{.Host}}
http://:{{.Port}} {
	bind 127.0.0.1
	root * {{q .DocRoot}}
	php_server {
{{if .HTTPS}}		env HTTPS on
		env REQUEST_SCHEME https
{{end}}		env SERVER_PORT {{.PublicPort}}
		env SERVER_NAME {{.Host}}
	}
}
{{end}}`))

// Dir is FrankenPHP's working folder inside Nido: generated\frankenphp (Caddyfile, Caddy's data).
func Dir(home paths.Home) string { return home.Generated("frankenphp") }

// ConfPath is the generated Caddyfile.
func ConfPath(home paths.Home) string { return filepath.Join(Dir(home), "Caddyfile") }

// Write renders the Caddyfile.
func Write(o Options) (string, error) {
	for _, d := range []string{filepath.Join(Dir(o.Home), "data"), filepath.Join(Dir(o.Home), "config")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}
	sites := make([]Site, len(o.Sites))
	for i, s := range o.Sites {
		s.DocRoot = paths.Slash(s.DocRoot)
		sites[i] = s
	}
	var buf bytes.Buffer
	err := caddyTemplate.Execute(&buf, map[string]any{
		"Now":       time.Now().Format("2006-01-02 15:04:05"),
		"AdminPort": o.AdminPort,
		"Sites":     sites,
	})
	if err != nil {
		return "", err
	}
	conf := ConfPath(o.Home)
	return conf, config.WriteFileAtomic(conf, buf.Bytes())
}

func exe(v versions.Version) string { return filepath.Join(v.Path, "frankenphp.exe") }

// env keeps Caddy's data inside Nido (instead of %AppData%\Caddy) and points PHP to Nido's php.ini.
func env(o Options) []string {
	return []string{
		"PHPRC=" + o.IniDir,
		"XDG_DATA_HOME=" + filepath.Join(Dir(o.Home), "data"),
		"XDG_CONFIG_HOME=" + filepath.Join(Dir(o.Home), "config"),
	}
}

func run(o Options, args ...string) (string, error) {
	return procutil.OutputEnv(60*time.Second, o.Version.Path, env(o), exe(o.Version), args...)
}

func adminAddress(o Options) string { return fmt.Sprintf("127.0.0.1:%d", o.AdminPort) }

// Test validates the Caddyfile so a broken configuration never replaces a working one.
func Test(o Options) error {
	if out, err := run(o, "validate", "--config", ConfPath(o.Home), "--adapter", "caddyfile"); err != nil {
		return fmt.Errorf("la configuración de FrankenPHP no es válida: %s", procutil.LastLines(out, 6))
	}
	return nil
}

// Reload makes the running FrankenPHP load the Caddyfile again without dropping requests.
func Reload(o Options) error {
	if out, err := run(o, "reload", "--config", ConfPath(o.Home), "--adapter", "caddyfile", "--address", adminAddress(o)); err != nil {
		return fmt.Errorf("FrankenPHP no se recargó: %s", procutil.LastLines(out, 4))
	}
	return nil
}

// Spec describes the FrankenPHP service; it stops gracefully through the admin API.
func Spec(o Options) supervisor.Spec {
	return supervisor.Spec{
		Name:    ServiceName,
		Exe:     exe(o.Version),
		Args:    []string{"run", "--config", ConfPath(o.Home), "--adapter", "caddyfile"},
		Dir:     o.Version.Path,
		Env:     env(o),
		LogPath: o.Home.Logs("frankenphp.log"),
		Port:    o.AdminPort,
		Restart: true,
		Stop: func(int) error {
			_, err := run(o, "stop", "--address", adminAddress(o))
			return err
		},
		StopTimeout: 20 * time.Second,
	}
}
