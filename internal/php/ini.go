// Package php generates php.ini files and runs the FastCGI pool of the global PHP version.
package php

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

// Extensions enabled by default when the build ships them (order matters: mbstring before exif).
var defaultExtensions = []string{
	"bz2", "curl", "fileinfo", "gd", "gd2", "gettext", "intl", "mbstring", "exif",
	"mysqli", "openssl", "pdo_mysql", "pdo_pgsql", "pdo_sqlite", "pgsql", "sodium", "sqlite3", "xsl", "zip",
}

// IniPath is where the generated php.ini of a version lives.
func IniPath(home paths.Home, v versions.Version) string {
	return home.Generated("php", v.Version, "php.ini")
}

// IniOptions are the values clon manages for every PHP version.
type IniOptions struct {
	CABundle string // PEM bundle with the public roots plus the local CA; empty to leave curl defaults
	SMTPPort int    // mail() goes to 127.0.0.1:SMTPPort (Mailpit); 0 leaves PHP's default
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
	b.WriteString("; clon: valores gestionados. Este archivo se regenera; pon tus cambios en\r\n")
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

	enabled := map[string]bool{}
	for _, ext := range defaultExtensions {
		if (ext == "gd2" && enabled["gd"]) || !hasExtension(v, ext) {
			continue
		}
		enabled[ext] = true
		set("extension", "php_"+ext+".dll")
	}
	if hasExtension(v, "opcache") {
		set("zend_extension", "php_opcache.dll")
	}
	set("opcache.enable", "1")
	set("opcache.enable_cli", "0")
	set("opcache.validate_timestamps", "1")
	set("opcache.revalidate_freq", "0")

	for _, extra := range []string{home.Config("php", "all.ini"), home.Config("php", versions.MajorMinor(v.Version)+".ini")} {
		if data, err := os.ReadFile(extra); err == nil {
			b.WriteString("\r\n; ---- " + extra + " ----\r\n")
			b.Write(data)
			b.WriteString("\r\n")
		}
	}

	out := IniPath(home, v)
	if err := config.WriteFileAtomic(out, []byte(b.String())); err != nil {
		return "", err
	}
	return out, nil
}

func hasExtension(v versions.Version, name string) bool {
	_, err := os.Stat(filepath.Join(v.Path, "ext", "php_"+name+".dll"))
	return err == nil
}
