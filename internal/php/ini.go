// Package php generates php.ini files and runs the FastCGI pool of the global PHP version.
package php

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

// Extensions enabled by default when the build ships them.
var defaultExtensions = []string{
	"bz2", "curl", "fileinfo", "gd", "gd2", "gettext", "intl", "mbstring", "exif",
	"mysqli", "openssl", "pdo_mysql", "pdo_pgsql", "pdo_sqlite", "pgsql", "sodium", "sqlite3", "xsl", "zip",
	"opcache",
}

// zendExtensions load with zend_extension, OPcache before Xdebug (as Xdebug asks).
var zendExtensions = []string{"opcache", "xdebug"}

// IniPath is where the generated php.ini of a version lives.
func IniPath(home paths.Home, v versions.Version) string {
	return home.Generated("php", v.Version, "php.ini")
}

// IniOptions are the values Nido manages for every PHP version.
type IniOptions struct {
	CABundle string // PEM bundle with the public roots plus the local CA; empty to leave curl defaults
	SMTPPort int    // mail() goes to 127.0.0.1:SMTPPort (Mailpit); 0 leaves PHP's default
	// Extensions changes the default extensions per series ("8.4").
	Extensions map[string]config.PHPExtensions
	// Builtin marks a PHP that carries its extensions inside (FrankenPHP's): no DLLs are loaded.
	Builtin bool
	// Debug writes the php.ini of the version's debugging pool and of phpx: Xdebug loaded and waiting
	// for the editor on 127.0.0.1:9003. It lives next to the normal one (<version>-debug).
	Debug bool
}

// DebugIniPath is where the php.ini with Xdebug of a version lives.
func DebugIniPath(home paths.Home, v versions.Version) string {
	return home.Generated("php", v.Version+"-debug", "php.ini")
}

// HasXdebug reports whether Xdebug is available to a PHP version (in its folder or downloaded).
func HasXdebug(home paths.Home, v versions.Version) bool {
	for _, x := range Extensions(home, v, config.PHPExtensions{}) {
		if x.Name == "xdebug" {
			return true
		}
	}
	return false
}

// ExtDir is where Nido keeps the extensions it downloads for a PHP series and thread safety
// ("8.4-ts"): never inside the PHP folder, which may not be Nido's.
func ExtDir(home paths.Home, v versions.Version) string {
	flavor := "nts"
	if v.ThreadSafe != nil && *v.ThreadSafe {
		flavor = "ts"
	}
	return home.Bin("php-ext", versions.MajorMinor(v.Version)+"-"+flavor)
}

// Extension is one extension a PHP version can load.
type Extension struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Default bool   `json:"default"`        // Nido enables it unless it is switched off
	Zend    bool   `json:"zend,omitempty"` // loaded with zend_extension
	Source  string `json:"source"`         // "incluida" (in the PHP folder) or "descargada"
	File    string `json:"-"`
}

// IsZend reports whether an extension loads with zend_extension.
func IsZend(name string) bool { return contains(zendExtensions, name) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Extensions lists what a PHP version can load: the DLLs in its ext folder and the ones Nido
// downloaded for it, each enabled by Nido's defaults or by the user's changes.
func Extensions(home paths.Home, v versions.Version, changes config.PHPExtensions) []Extension {
	seen := map[string]bool{}
	var out []Extension
	for _, dir := range []struct{ path, source string }{{filepath.Join(v.Path, "ext"), "incluida"}, {ExtDir(home, v), "descargada"}} {
		entries, _ := os.ReadDir(dir.path)
		for _, e := range entries {
			n := strings.ToLower(e.Name())
			if e.IsDir() || !strings.HasPrefix(n, "php_") || !strings.HasSuffix(n, ".dll") {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(n, "php_"), ".dll")
			if seen[name] {
				continue
			}
			seen[name] = true
			def := contains(defaultExtensions, name)
			out = append(out, Extension{
				Name: name, Default: def, Zend: contains(zendExtensions, name), Source: dir.source,
				Enabled: (def || contains(changes.Enable, name)) && !contains(changes.Disable, name),
				File:    filepath.Join(dir.path, e.Name()),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// loadOrder puts mbstring first (exif needs it in old versions), then the other extensions, then
// the zend ones in their own order.
func loadOrder(list []Extension) []Extension {
	rank := func(e Extension) int {
		switch {
		case e.Name == "mbstring":
			return 0
		case e.Zend:
			for i, z := range zendExtensions {
				if z == e.Name {
					return 2 + i
				}
			}
			return 2 + len(zendExtensions)
		}
		return 1
	}
	out := append([]Extension(nil), list...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

// WriteIni builds php.ini from the version's php.ini-development, the managed values and the user's
// overrides in config\php\all.ini and config\php\<major.minor>.ini (those always win).
func WriteIni(home paths.Home, v versions.Version, opt IniOptions) (string, error) {
	base, _ := os.ReadFile(filepath.Join(v.Path, "php.ini-development"))
	for _, d := range []string{home.Tmp("php-sessions"), home.Tmp("php-uploads"), home.Logs()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	b.Write(base)
	b.WriteString("\r\n\r\n; ======================================================================\r\n")
	b.WriteString("; Nido: valores gestionados. Este archivo se regenera; pon tus cambios en\r\n")
	b.WriteString(fmt.Sprintf("; %s o %s\r\n", home.Config("php", "all.ini"), home.Config("php", versions.MajorMinor(v.Version)+".ini")))
	b.WriteString("; ======================================================================\r\n[PHP]\r\n")
	set := func(k, val string) { b.WriteString(k + " = " + val + "\r\n") }
	q := func(p string) string { return `"` + paths.Slash(p) + `"` }

	set("extension_dir", q(filepath.Join(v.Path, "ext")))
	set("error_log", q(home.Logs("php-"+v.Version+".log")))
	set("log_errors", "On")
	set("memory_limit", "512M")
	set("upload_max_filesize", "512M")
	set("post_max_size", "512M")
	set("max_execution_time", "300")
	set("max_input_vars", "5000")
	// php-cgi rereads a site's .user.ini at most once a second instead of every 5 minutes: a change
	// made in the site's page counts at the next request.
	set("user_ini.cache_ttl", "0")
	set("session.save_path", q(home.Tmp("php-sessions")))
	set("upload_tmp_dir", q(home.Tmp("php-uploads")))
	set("sys_temp_dir", q(home.Tmp()))
	if opt.CABundle != "" {
		set("curl.cainfo", q(opt.CABundle))
		set("openssl.cafile", q(opt.CABundle))
	}
	if opt.SMTPPort > 0 {
		set("SMTP", "127.0.0.1")
		set("smtp_port", fmt.Sprint(opt.SMTPPort))
	}

	if !opt.Builtin {
		extDir := filepath.Join(v.Path, "ext")
		debugReady := false
		for _, e := range loadOrder(Extensions(home, v, opt.Extensions[versions.MajorMinor(v.Version)])) {
			if opt.Debug && e.Name == "xdebug" {
				e.Enabled, debugReady = true, true
			}
			if !e.Enabled || (e.Name == "gd2" && hasExtension(v, "gd")) {
				continue
			}
			value := filepath.Base(e.File)
			if !strings.EqualFold(filepath.Dir(e.File), extDir) {
				value = q(e.File) // downloaded by Nido, outside the PHP folder
			}
			if e.Zend {
				set("zend_extension", value)
			} else {
				set("extension", value)
			}
		}
		if opt.Debug && !debugReady {
			return "", fmt.Errorf("PHP %s no tiene Xdebug: actívalo en «Extensiones de PHP» o con 'nido php ext on xdebug %s'", v.Version, versions.MajorMinor(v.Version))
		}
	}
	set("opcache.enable", "1")
	set("opcache.enable_cli", "0")
	set("opcache.validate_timestamps", "1")
	set("opcache.revalidate_freq", "0")
	if opt.Debug {
		// Every request starts a debugging session with the editor listening on 9003 (VS Code,
		// PhpStorm); without one listening, PHP goes on after a short timeout.
		set("xdebug.mode", "debug,develop")
		set("xdebug.start_with_request", "yes")
		set("xdebug.client_host", "127.0.0.1")
		set("xdebug.client_port", "9003")
		set("xdebug.connect_timeout_ms", "200")
		set("xdebug.log_level", "0")
	}

	for _, extra := range []string{home.Config("php", "all.ini"), home.Config("php", versions.MajorMinor(v.Version)+".ini")} {
		if data, err := os.ReadFile(extra); err == nil {
			b.WriteString("\r\n; ---- " + extra + " ----\r\n")
			b.Write(data)
			b.WriteString("\r\n")
		}
	}

	out := IniPath(home, v)
	if opt.Debug {
		out = DebugIniPath(home, v)
	}
	if err := config.WriteFileAtomic(out, []byte(b.String())); err != nil {
		return "", err
	}
	return out, nil
}

func hasExtension(v versions.Version, name string) bool {
	_, err := os.Stat(filepath.Join(v.Path, "ext", "php_"+name+".dll"))
	return err == nil
}
