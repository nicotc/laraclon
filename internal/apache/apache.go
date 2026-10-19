// Package apache writes a complete httpd.conf (one internal port per site, PHP over FastCGI) and
// controls Apache's Windows server process.
package apache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// ServiceName is the supervisor name of the Apache process.
const ServiceName = "apache"

// Site is what Apache needs to know about one site.
type Site struct {
	Host    string
	Aliases []string
	DocRoot string
	Port    int
	HTTPS   bool
	Pool    string // the PHP pool of the site ("" = the global one)
	Extra   string // file with the site's own directives, included at the end of its VirtualHost
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
	AdminPort int // internal health-check port, always present so Apache has a listener
	Pools     []Pool
	Sites     []Site
}

type module struct{ Name, Path string }

var requiredModules = []string{
	"authz_core", "authz_host", "dir", "mime", "log_config", "setenvif", "env", "alias",
	"rewrite", "headers", "proxy", "proxy_fcgi", "proxy_balancer", "slotmem_shm", "lbmethod_bybusyness",
}

// Loaded when the build has them: common in .htaccess files of real projects.
var optionalModules = []string{
	"access_compat", "authn_core", "authn_file", "auth_basic", "authz_user", "autoindex",
	"deflate", "filter", "expires", "include", "actions", "negotiation", "version", "mime_magic",
}

var confTemplate = template.Must(template.New("httpd").Parse(`# Generado por Nido el {{.Now}}. No lo edites: se regenera al cambiar sitios o versiones.
Define SRVROOT "{{.Root}}"
ServerRoot "{{.Root}}"
DefaultRuntimeDir "{{.RunDir}}"
PidFile "{{.RunDir}}/httpd.pid"
ServerName localhost
ServerAdmin admin@localhost
ServerSignature Off
ServerTokens Prod
HostnameLookups Off
Timeout 3600
KeepAlive On
ThreadsPerChild 150
AcceptFilter http none
AcceptFilter https none
EnableSendfile Off
EnableMMAP Off

{{range .Modules}}LoadModule {{.Name}}_module "{{.Path}}"
{{end}}
TypesConfig "{{.Root}}/conf/mime.types"
DirectoryIndex index.php index.html index.htm
ErrorLog "{{.Logs}}/apache-error.log"
LogLevel {{.LogLevel}}
LogFormat "%h %t \"%r\" %>s %b %D \"%{Host}i\" \"%{User-Agent}i\"" Nido
CustomLog "{{.Logs}}/apache-access.log" Nido

<Directory />
    AllowOverride None
    Require all denied
</Directory>
<Files ".ht*">
    Require all denied
</Files>

# PHP: un grupo de php-cgi por versión (el PHP global y el que fija algún sitio), con reparto por ocupación
ProxyTimeout 3600
# mod_proxy_fcgi sends SCRIPT_FILENAME as "proxy:fcgi://127.0.0.1:9100/E:/…" (PHP-FPM strips that
# prefix itself; php-cgi on Windows does not), so Nido hands php-cgi the plain "E:/…" path.
ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m#^(?:proxy:fcgi://[^/]*)?/([A-Za-z]:/.*)$#" SCRIPT_FILENAME "$1"
ProxyFCGISetEnvIf "reqenv('PATH_TRANSLATED') =~ m#^(?:proxy:fcgi://[^/]*)?/([A-Za-z]:/.*)$#" PATH_TRANSLATED "$1"
{{range .Pools}}<Proxy "balancer://{{.Name}}">
{{range .Ports}}    BalancerMember "fcgi://127.0.0.1:{{.}}" retry=1
{{end}}    ProxySet lbmethod=bybusyness
</Proxy>
{{end}}

# Comprobación de salud de Nido
Listen 127.0.0.1:{{.AdminPort}}
<VirtualHost 127.0.0.1:{{.AdminPort}}>
    ServerName nido-apache.localhost
    DocumentRoot "{{.EmptyRoot}}"
    <Directory "{{.EmptyRoot}}">
        Require all granted
    </Directory>
</VirtualHost>
{{range .Sites}}
# {{.Host}}
Listen 127.0.0.1:{{.Port}}
<VirtualHost 127.0.0.1:{{.Port}}>
    ServerName {{if .HTTPS}}https://{{.Host}}:443{{else}}http://{{.Host}}:80{{end}}
    ServerAlias *.{{.Host}}{{range .Aliases}} {{.}}{{end}}
    DocumentRoot "{{.DocRoot}}"
    <Directory "{{.DocRoot}}">
        Options Indexes FollowSymLinks
        AllowOverride All
        Require all granted
    </Directory>
    SetEnvIf X-Forwarded-Proto "^https$" HTTPS=on
    <FilesMatch "\.php$">
        <If "-f %{REQUEST_FILENAME}">
            # The trailing slash matters: Apache appends the script path to this URL.
            SetHandler "proxy:balancer://{{.Pool}}/"
        </If>
    </FilesMatch>
{{if .Extra}}    # Directivas propias del sitio (su ficha en el panel)
    Include "{{.Extra}}"
{{end}}</VirtualHost>
{{end}}`))

// ConfPath is where the generated httpd.conf of a version lives.
func ConfPath(home paths.Home, v versions.Version) string {
	return home.Generated("apache", v.Version, "httpd.conf")
}

// Write renders httpd.conf.
func Write(o Options) (string, error) {
	conf := ConfPath(o.Home, o.Version)
	return conf, WriteTo(o, conf)
}

// WriteTo renders the configuration into another file, to test it without touching the real one.
func WriteTo(o Options, conf string) error {
	var mods []module
	for _, name := range requiredModules {
		p := filepath.Join(o.Version.Path, "modules", "mod_"+name+".so")
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("a Apache %s le falta el módulo mod_%s", o.Version.Version, name)
		}
		mods = append(mods, module{name, paths.Slash(p)})
	}
	for _, name := range optionalModules {
		p := filepath.Join(o.Version.Path, "modules", "mod_"+name+".so")
		if _, err := os.Stat(p); err == nil {
			mods = append(mods, module{name, paths.Slash(p)})
		}
	}
	runDir := o.Home.Run("apache")
	empty := o.Home.Generated("apache", "empty")
	for _, d := range []string{runDir, empty, o.Home.Logs()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	known := map[string]bool{}
	for _, p := range o.Pools {
		known[p.Name] = len(p.Ports) > 0
	}
	if !known[GlobalPool] {
		return fmt.Errorf("falta el grupo de PHP global")
	}
	sites := make([]Site, len(o.Sites))
	for i, s := range o.Sites {
		s.DocRoot = paths.Slash(s.DocRoot)
		s.Extra = paths.Slash(s.Extra)
		if !known[s.Pool] {
			s.Pool = GlobalPool
		}
		sites[i] = s
	}
	// NIDO_APACHE_LOGLEVEL (for example "warn proxy_fcgi:trace8") helps diagnose FastCGI problems.
	logLevel := "warn"
	if v := os.Getenv("NIDO_APACHE_LOGLEVEL"); v != "" {
		logLevel = v
	}
	var buf bytes.Buffer
	err := confTemplate.Execute(&buf, map[string]any{
		"LogLevel":  logLevel,
		"Now":       time.Now().Format("2006-01-02 15:04:05"),
		"Root":      paths.Slash(o.Version.Path),
		"RunDir":    paths.Slash(runDir),
		"Logs":      paths.Slash(o.Home.Logs()),
		"EmptyRoot": paths.Slash(empty),
		"Modules":   mods,
		"Pools":     o.Pools,
		"AdminPort": o.AdminPort,
		"Sites":     sites,
	})
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(conf, buf.Bytes())
}

func httpd(v versions.Version) string { return filepath.Join(v.Path, "bin", "httpd.exe") }

// Test runs httpd -t so a broken configuration never replaces a working one.
func Test(v versions.Version, conf string) error {
	out, err := procutil.Output(30*time.Second, v.Path, httpd(v), "-t", "-f", conf)
	if err != nil {
		return fmt.Errorf("la configuración de Apache no es válida: %s", procutil.LastLines(out, 6))
	}
	return nil
}

// Spec describes the Apache service.
func Spec(o Options, conf string) supervisor.Spec {
	return supervisor.Spec{
		Name:        ServiceName,
		Exe:         httpd(o.Version),
		Args:        []string{"-f", conf},
		Dir:         o.Version.Path,
		LogPath:     o.Home.Logs("apache.log"),
		Port:        o.AdminPort,
		Restart:     true,
		Stop:        GracefulStop,
		StopTimeout: 15 * time.Second,
	}
}

// Apache's Windows MPM listens on named events "ap<parent pid>_restart" and "ap<parent pid>_shutdown".
func signal(pid int, what string) error {
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("ap%d_%s", pid, what))
	if err != nil {
		return err
	}
	// Right after Apache starts (or another change restarts it), its parent creates these events a
	// moment later: a signal sent meanwhile waits for them instead of failing.
	var h windows.Handle
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		h, err = windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
		if err == nil || err != windows.ERROR_FILE_NOT_FOUND || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("Apache (PID %d) no acepta la señal %s: %w", pid, what, err)
	}
	defer windows.CloseHandle(h)
	return windows.SetEvent(h)
}

// GracefulRestart reloads the configuration without dropping requests in flight. Apache answers
// with the new configuration once a new child process takes over, which this waits for (a few
// seconds at most), so the caller can rely on it.
func GracefulRestart(pid int) error {
	since := time.Now()
	if err := signal(pid, "restart"); err != nil {
		return err
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if childSince(pid, since) {
			break
		}
	}
	return nil
}

// childSince reports whether the process has a child created after since.
func childSince(parent int, since time.Time) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ParentProcessID) != parent {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
		if err != nil {
			continue
		}
		var created, exited, kernel, user windows.Filetime
		err = windows.GetProcessTimes(h, &created, &exited, &kernel, &user)
		windows.CloseHandle(h)
		if err == nil && time.Unix(0, created.Nanoseconds()).After(since) {
			return true
		}
	}
	return false
}

// GracefulStop lets current requests finish, then exits.
func GracefulStop(pid int) error { return signal(pid, "shutdown") }
