package versions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/procutil"
)

const probeTimeout = 20 * time.Second

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// DetectPHP inspects a PHP folder by asking php.exe (without any php.ini) for its build details.
func DetectPHP(dir string) (Version, error) {
	exe := filepath.Join(dir, "php.exe")
	if !exists(exe) || !exists(filepath.Join(dir, "php-cgi.exe")) {
		return Version{}, fmt.Errorf("no hay php.exe y php-cgi.exe en %s", dir)
	}
	out, err := procutil.Output(probeTimeout, dir, exe, "-n", "-r", `echo PHP_VERSION, "|", PHP_ZTS, "|", PHP_INT_SIZE * 8;`)
	if err != nil {
		return Version{}, err
	}
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("respuesta inesperada de php.exe: %q", out)
	}
	ts := parts[1] == "1"
	arch := "x64"
	if parts[2] == "32" {
		arch = "x86"
	}
	return Version{Component: PHP, Version: parts[0], Path: dir, ThreadSafe: &ts, Arch: arch}, nil
}

var apacheVersionRe = regexp.MustCompile(`Apache/(\d+\.\d+\.\d+)\s*\((Win32|Win64)\)?`)

// DetectApache accepts either the ServerRoot (with bin\httpd.exe) or a folder that contains it (Apache24\).
func DetectApache(dir string) (Version, error) {
	root := ""
	for _, cand := range []string{dir, filepath.Join(dir, "Apache24"), filepath.Join(dir, "Apache2")} {
		if exists(filepath.Join(cand, "bin", "httpd.exe")) {
			root = cand
			break
		}
	}
	if root == "" {
		return Version{}, fmt.Errorf("no hay bin\\httpd.exe en %s", dir)
	}
	out, err := procutil.Output(probeTimeout, root, filepath.Join(root, "bin", "httpd.exe"), "-v")
	if err != nil {
		return Version{}, err
	}
	m := apacheVersionRe.FindStringSubmatch(out)
	if m == nil {
		return Version{}, fmt.Errorf("versión de Apache no reconocida: %q", out)
	}
	arch := "x64"
	if m[2] == "Win32" {
		arch = "x86"
	}
	return Version{Component: Apache, Version: m[1], Path: root, Arch: arch}, nil
}

var mysqlVersionRe = regexp.MustCompile(`Ver (\d+\.\d+\.\d+)(-MariaDB)?`)

// DetectMySQL recognises MySQL and MariaDB folders (bin\mysqld.exe, or bin\mariadbd.exe in the
// MariaDB versions that dropped the old name).
func DetectMySQL(dir string) (Version, error) {
	exe := filepath.Join(dir, "bin", "mysqld.exe")
	if !exists(exe) {
		exe = filepath.Join(dir, "bin", "mariadbd.exe")
	}
	if !exists(exe) {
		return Version{}, fmt.Errorf("no hay bin\\mysqld.exe ni bin\\mariadbd.exe en %s", dir)
	}
	out, err := procutil.Output(probeTimeout, dir, exe, "--version")
	if err != nil {
		return Version{}, err
	}
	m := mysqlVersionRe.FindStringSubmatch(out)
	if m == nil {
		return Version{}, fmt.Errorf("versión de MySQL no reconocida: %q", out)
	}
	c := MySQL
	if m[2] != "" || strings.Contains(out, "MariaDB") {
		c = MariaDB
	}
	return Version{Component: c, Version: m[1], Path: dir, Arch: "x64"}, nil
}

// DetectNode reads node.exe -v.
func DetectNode(dir string) (Version, error) {
	exe := filepath.Join(dir, "node.exe")
	if !exists(exe) {
		return Version{}, fmt.Errorf("no hay node.exe en %s", dir)
	}
	out, err := procutil.Output(probeTimeout, dir, exe, "-v")
	if err != nil {
		return Version{}, err
	}
	v := strings.TrimPrefix(strings.TrimSpace(out), "v")
	if v == "" {
		return Version{}, fmt.Errorf("versión de Node no reconocida")
	}
	return Version{Component: Node, Version: v, Path: dir, Arch: "x64"}, nil
}

var nginxVersionRe = regexp.MustCompile(`nginx/(\d+\.\d+\.\d+)`)

func DetectNginx(dir string) (Version, error) {
	exe := filepath.Join(dir, "nginx.exe")
	if !exists(exe) {
		return Version{}, fmt.Errorf("no hay nginx.exe en %s", dir)
	}
	out, err := procutil.Output(probeTimeout, dir, exe, "-v")
	if err != nil {
		return Version{}, err
	}
	m := nginxVersionRe.FindStringSubmatch(out)
	if m == nil {
		return Version{}, fmt.Errorf("versión de Nginx no reconocida: %q", out)
	}
	return Version{Component: Nginx, Version: m[1], Path: dir}, nil
}

var redisVersionRe = regexp.MustCompile(`v=(\d+\.\d+\.\d+(?:\.\d+)?)`)

func DetectRedis(dir string) (Version, error) {
	exe := filepath.Join(dir, "redis-server.exe")
	if !exists(exe) {
		return Version{}, fmt.Errorf("no hay redis-server.exe en %s", dir)
	}
	out, err := procutil.Output(probeTimeout, dir, exe, "--version")
	if err != nil {
		return Version{}, err
	}
	m := redisVersionRe.FindStringSubmatch(out)
	if m == nil {
		return Version{}, fmt.Errorf("versión de Redis no reconocida: %q", out)
	}
	return Version{Component: Redis, Version: m[1], Path: dir}, nil
}

var mongoRe = regexp.MustCompile(`db version v(\d+\.\d+\.\d+)`)

// DetectMongoDB reads "mongod --version" ("db version v9.0.4").
func DetectMongoDB(dir string) (Version, error) {
	exe := filepath.Join(dir, "bin", "mongod.exe")
	if !exists(exe) {
		return Version{}, fmt.Errorf("no hay bin\\mongod.exe en %s", dir)
	}
	out, err := procutil.Output(probeTimeout, dir, exe, "--version")
	if err != nil {
		return Version{}, err
	}
	m := mongoRe.FindStringSubmatch(out)
	if m == nil {
		return Version{}, fmt.Errorf("versión de MongoDB no reconocida: %q", out)
	}
	return Version{Component: MongoDB, Version: m[1], Path: dir, Arch: "x64"}, nil
}

var frankenRe = regexp.MustCompile(`FrankenPHP v?(\d+\.\d+\.\d+)\s+PHP (\d+\.\d+\.\d+)`)

// DetectFrankenPHP reads "frankenphp version" ("FrankenPHP 1.13.1 PHP 8.5.11 Caddy v2.11.7 …").
func DetectFrankenPHP(dir string) (Version, error) {
	exe := filepath.Join(dir, "frankenphp.exe")
	if !exists(exe) {
		return Version{}, fmt.Errorf("no hay frankenphp.exe en %s", dir)
	}
	out, err := procutil.Output(probeTimeout, dir, exe, "version")
	if err != nil {
		return Version{}, err
	}
	m := frankenRe.FindStringSubmatch(out)
	if m == nil {
		return Version{}, fmt.Errorf("versión de FrankenPHP no reconocida: %q", out)
	}
	return Version{Component: FrankenPHP, Version: m[1], Path: dir, Arch: "x64", PHP: m[2]}, nil
}

// Detect dispatches on the component kind.
func Detect(c Component, dir string) (Version, error) {
	switch c {
	case PHP:
		return DetectPHP(dir)
	case Apache:
		return DetectApache(dir)
	case MySQL, MariaDB:
		return DetectMySQL(dir)
	case Node:
		return DetectNode(dir)
	case Nginx:
		return DetectNginx(dir)
	case Redis:
		return DetectRedis(dir)
	case FrankenPHP:
		return DetectFrankenPHP(dir)
	case MongoDB:
		return DetectMongoDB(dir)
	}
	return Version{}, fmt.Errorf("componente desconocido: %s", c)
}
