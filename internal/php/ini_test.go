package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestWriteIniLoadsTheChosenExtensionsInOrder(t *testing.T) {
	home := paths.Home{Root: t.TempDir()}
	ts := true
	v := versions.Version{Component: versions.PHP, Version: "8.4.16", Path: t.TempDir(), ThreadSafe: &ts}
	touch := func(dir, name string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"php_exif.dll", "php_mbstring.dll", "php_ldap.dll", "php_opcache.dll", "php_curl.dll"} {
		touch(filepath.Join(v.Path, "ext"), n)
	}
	touch(ExtDir(home, v), "php_xdebug.dll") // downloaded by clon, outside the PHP folder

	changes := config.PHPExtensions{Enable: []string{"ldap", "xdebug"}, Disable: []string{"exif"}}
	ini, err := WriteIni(home, v, IniOptions{Extensions: map[string]config.PHPExtensions{"8.4": changes}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(ini)
	var loaded []string
	for _, line := range strings.Split(string(data), "\r\n") {
		if strings.HasPrefix(line, "extension = ") || strings.HasPrefix(line, "zend_extension = ") {
			loaded = append(loaded, line)
		}
	}
	xdebug := `zend_extension = "` + paths.Slash(filepath.Join(ExtDir(home, v), "php_xdebug.dll")) + `"`
	want := []string{
		"extension = php_mbstring.dll", // first: exif needs it in old versions
		"extension = php_curl.dll",
		"extension = php_ldap.dll",
		"zend_extension = php_opcache.dll", // OPcache before Xdebug
		xdebug,
	}
	if strings.Join(loaded, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(loaded, "\n"), strings.Join(want, "\n"))
	}

	// FrankenPHP's PHP carries its extensions inside: no DLL is loaded for it.
	ini, err = WriteIni(home, v, IniOptions{Builtin: true})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(ini); strings.Contains(string(data), "extension = ") {
		t.Fatal("a built-in PHP must not load DLLs")
	}
}

func TestDebugIniLoadsXdebugApart(t *testing.T) {
	home := paths.Home{Root: t.TempDir()}
	ts := true
	v := versions.Version{Component: versions.PHP, Version: "8.4.16", Path: t.TempDir(), ThreadSafe: &ts}
	os.MkdirAll(filepath.Join(v.Path, "ext"), 0o755)
	os.WriteFile(filepath.Join(v.Path, "ext", "php_opcache.dll"), nil, 0o644)
	if _, err := WriteIni(home, v, IniOptions{Debug: true}); err == nil {
		t.Fatal("without Xdebug, the debug php.ini cannot be written")
	}
	os.MkdirAll(ExtDir(home, v), 0o755)
	os.WriteFile(filepath.Join(ExtDir(home, v), "php_xdebug.dll"), nil, 0o644)
	if !HasXdebug(home, v) {
		t.Fatal("the downloaded Xdebug must be seen")
	}
	normal, _ := WriteIni(home, v, IniOptions{})
	debug, err := WriteIni(home, v, IniOptions{Debug: true})
	if err != nil || normal == debug || debug != DebugIniPath(home, v) {
		t.Fatalf("normal %s, debug %s, %v", normal, debug, err)
	}
	n, _ := os.ReadFile(normal)
	d, _ := os.ReadFile(debug)
	if strings.Contains(string(n), "php_xdebug.dll") || strings.Contains(string(n), "xdebug.mode") {
		t.Error("the normal php.ini stays without Xdebug")
	}
	for _, want := range []string{"php_xdebug.dll", "xdebug.mode = debug,develop", "xdebug.start_with_request = yes", "xdebug.client_port = 9003"} {
		if !strings.Contains(string(d), want) {
			t.Errorf("the debug php.ini lacks %q", want)
		}
	}
}

func TestExtensionsListsWhereEachOneComesFrom(t *testing.T) {
	home := paths.Home{Root: t.TempDir()}
	v := versions.Version{Component: versions.PHP, Version: "8.3.12", Path: t.TempDir()}
	for dir, name := range map[string]string{filepath.Join(v.Path, "ext"): "php_soap.dll", ExtDir(home, v): "php_mongodb.dll"} {
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, name), nil, 0o644)
	}
	got := map[string]Extension{}
	for _, x := range Extensions(home, v, config.PHPExtensions{}) {
		got[x.Name] = x
	}
	if x := got["soap"]; x.Source != "incluida" || x.Enabled || x.Default {
		t.Errorf("soap: %+v", x)
	}
	if x := got["mongodb"]; x.Source != "descargada" || x.Enabled {
		t.Errorf("mongodb: %+v", x)
	}
	if !strings.HasSuffix(ExtDir(home, v), "8.3-nts") {
		t.Errorf("an NTS build keeps its downloads in 8.3-nts, got %s", ExtDir(home, v))
	}
}
