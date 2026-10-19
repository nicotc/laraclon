// Package nginx writes nginx.conf (one internal port per site that chose Nginx, PHP over FastCGI to
// the pool of the global PHP or of the version the site pinned) and controls nginx for Windows.
package nginx

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// ServiceName is the supervisor name of the nginx process.
const ServiceName = "nginx"

// Site is what Nginx needs to know about one site.
type Site struct {
	Host       string
	Aliases    []string
	DocRoot    string
	Port       int
	HTTPS      bool
	PublicPort int    // the port the browser uses (443, or the proxy's), handed to PHP as SERVER_PORT
	Pool       string // the PHP pool of the site ("" = the global one)
}

// Pool is a group of php-cgi processes: the global PHP's, or one of a version some site pinned.
type Pool struct {
	Name  string // "php" for the global PHP
	Ports []int
}

// GlobalPool is the name of the global PHP's pool.
const GlobalPool = "php"

// Options describe one generated configuration.
type Options struct {
	Home      paths.Home
	Version   versions.Version
	AdminPort int // health-check listener, so nginx always has one
	Pools     []Pool
	Sites     []Site
}

// upstream turns a pool name into an nginx upstream name ("php-7-2-0" → "clon_php_7_2_0").
func upstream(pool string) string { return "clon_" + strings.ReplaceAll(pool, "-", "_") }

var confTemplate = template.Must(template.New("nginx").Funcs(template.FuncMap{"q": strconv.Quote, "upstream": upstream}).Parse(`# Generado por clon el {{.Now}}. No lo edites: se regenera al cambiar sitios o versiones.
# nginx para Windows atiende con un solo proceso de trabajo.
worker_processes 1;
error_log {{q .ErrorLog}} warn;
pid {{q .Pid}};

events {
    worker_connections 1024;
}

http {
    include {{q .MimeTypes}};
    default_type application/octet-stream;
    sendfile off;
    keepalive_timeout 65;
    client_max_body_size 1024m;
    server_tokens off;
    access_log {{q .AccessLog}};

    client_body_temp_path {{q (printf "%s/client_body" .Temp)}};
    proxy_temp_path {{q (printf "%s/proxy" .Temp)}};
    fastcgi_temp_path {{q (printf "%s/fastcgi" .Temp)}};
    uwsgi_temp_path {{q (printf "%s/uwsgi" .Temp)}};
    scgi_temp_path {{q (printf "%s/scgi" .Temp)}};

    # PHP: los mismos grupos de php-cgi que usa Apache (el global y el de cada versión fijada). Cada
    # php-cgi atiende una petición a la vez; max_fails=0 evita que un cambio de PHP deje un proceso
    # marcado como caído 10 segundos.
{{range .Pools}}    upstream {{upstream .Name}} {
        least_conn;
{{range .Ports}}        server 127.0.0.1:{{.}} max_fails=0;
{{end}}    }
{{end}}
    # Comprobación de salud de clon
    server {
        listen 127.0.0.1:{{.AdminPort}};
        location / { return 204; }
    }
{{range .Sites}}
    # {{.Host}}
    server {
        listen 127.0.0.1:{{.Port}};
        server_name {{.Host}} *.{{.Host}}{{range .Aliases}} {{.}}{{end}};
        root {{q .DocRoot}};
        index index.php index.html index.htm;
        charset utf-8;

        location / {
            try_files $uri $uri/ /index.php?$query_string;
        }

        location ~ \.php$ {
            try_files $uri =404;
            fastcgi_split_path_info ^(.+\.php)(/.+)$;
            fastcgi_pass {{upstream .Pool}};
            fastcgi_index index.php;
            include {{q $.FastCGIParams}};
            fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
            fastcgi_param DOCUMENT_ROOT $document_root;
            fastcgi_param SERVER_NAME {{.Host}};
            fastcgi_param SERVER_PORT {{.PublicPort}};
{{if .HTTPS}}            fastcgi_param HTTPS on;
            fastcgi_param REQUEST_SCHEME https;
{{else}}            fastcgi_param REQUEST_SCHEME http;
{{end}}            fastcgi_read_timeout 3600;
        }

        location ~ /\.(?!well-known) {
            deny all;
        }
    }
{{end}}}
`))

// Prefix is nginx's working folder inside clon: generated\nginx (config, temp files).
func Prefix(home paths.Home) string { return home.Generated("nginx") }

// ConfPath is the generated nginx.conf.
func ConfPath(home paths.Home) string { return filepath.Join(Prefix(home), "nginx.conf") }

// Write renders nginx.conf and creates the folders nginx expects.
func Write(o Options) (string, error) {
	prefix := Prefix(o.Home)
	temp := filepath.Join(prefix, "temp")
	for _, d := range []string{filepath.Join(prefix, "logs"), temp, o.Home.Logs(), o.Home.Run()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}
	for _, d := range []string{"client_body", "proxy", "fastcgi", "uwsgi", "scgi"} {
		if err := os.MkdirAll(filepath.Join(temp, d), 0o755); err != nil {
			return "", err
		}
	}
	known := map[string]bool{}
	for _, p := range o.Pools {
		known[p.Name] = len(p.Ports) > 0
	}
	if !known[GlobalPool] {
		return "", fmt.Errorf("falta el grupo de PHP global")
	}
	sites := make([]Site, len(o.Sites))
	for i, s := range o.Sites {
		s.DocRoot = paths.Slash(s.DocRoot)
		if !known[s.Pool] {
			s.Pool = GlobalPool
		}
		sites[i] = s
	}
	var buf bytes.Buffer
	err := confTemplate.Execute(&buf, map[string]any{
		"Now":           time.Now().Format("2006-01-02 15:04:05"),
		"ErrorLog":      paths.Slash(o.Home.Logs("nginx-error.log")),
		"AccessLog":     paths.Slash(o.Home.Logs("nginx-access.log")),
		"Pid":           paths.Slash(o.Home.Run("nginx.pid")),
		"MimeTypes":     paths.Slash(filepath.Join(o.Version.Path, "conf", "mime.types")),
		"FastCGIParams": paths.Slash(filepath.Join(o.Version.Path, "conf", "fastcgi_params")),
		"Temp":          paths.Slash(temp),
		"Pools":         o.Pools,
		"AdminPort":     o.AdminPort,
		"Sites":         sites,
	})
	if err != nil {
		return "", err
	}
	conf := ConfPath(o.Home)
	return conf, config.WriteFileAtomic(conf, buf.Bytes())
}

func exe(v versions.Version) string { return filepath.Join(v.Path, "nginx.exe") }

// run executes nginx with clon's prefix and configuration plus extra arguments.
func run(home paths.Home, v versions.Version, args ...string) (string, error) {
	all := append([]string{"-p", paths.Slash(Prefix(home)) + "/", "-c", paths.Slash(ConfPath(home))}, args...)
	return procutil.Output(30*time.Second, v.Path, exe(v), all...)
}

// Test runs nginx -t so a broken configuration never replaces a working one.
func Test(home paths.Home, v versions.Version) error {
	if out, err := run(home, v, "-t"); err != nil {
		return fmt.Errorf("la configuración de Nginx no es válida: %s", procutil.LastLines(out, 6))
	}
	return nil
}

// Reload makes the running nginx read its configuration again without dropping requests.
func Reload(home paths.Home, v versions.Version) error {
	if out, err := run(home, v, "-s", "reload"); err != nil {
		return fmt.Errorf("nginx no se recargó: %s", procutil.LastLines(out, 4))
	}
	return nil
}

// Spec describes the nginx service; it stops gracefully with "nginx -s quit".
func Spec(o Options) supervisor.Spec {
	return supervisor.Spec{
		Name:    ServiceName,
		Exe:     exe(o.Version),
		Args:    []string{"-p", paths.Slash(Prefix(o.Home)) + "/", "-c", paths.Slash(ConfPath(o.Home))},
		Dir:     o.Version.Path,
		LogPath: o.Home.Logs("nginx.log"),
		Port:    o.AdminPort,
		Restart: true,
		Stop: func(int) error {
			_, err := run(o.Home, o.Version, "-s", "quit")
			return err
		},
		StopTimeout: 15 * time.Second,
	}
}
