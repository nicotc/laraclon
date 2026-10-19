package versions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectAdminerReadsTheHeader(t *testing.T) {
	dir := t.TempDir()
	head := "<?php\n/** Adminer - Compact database management\n* @link https://www.adminer.org/\n* @version 6.1.2\n*/namespace Adminer;"
	if err := os.WriteFile(filepath.Join(dir, "adminer.php"), []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := DetectAdminer(dir)
	if err != nil || v.Component != Adminer || v.Version != "6.1.2" {
		t.Fatalf("got %+v, %v", v, err)
	}
	if _, err := DetectAdminer(t.TempDir()); err == nil {
		t.Fatal("an empty folder has no Adminer")
	}
}

func TestProgramFindsTheMainExecutable(t *testing.T) {
	touch := func(parts ...string) string {
		p := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	lounge := t.TempDir() // Apache Lounge's zip keeps everything under Apache24\
	want := touch(lounge, "Apache24", "bin", "httpd.exe")
	if got := Program(Apache, lounge); got != want {
		t.Errorf("Apache: got %q, want %q", got, want)
	}
	maria := t.TempDir() // MariaDB 11+ may ship only the new name
	want = touch(maria, "bin", "mariadbd.exe")
	if got := Program(MySQL, maria); got != want {
		t.Errorf("MariaDB: got %q, want %q", got, want)
	}
	if got := Program(Nginx, t.TempDir()); got != "" {
		t.Errorf("an empty folder has no program, got %q", got)
	}
}

func TestCompareAndFind(t *testing.T) {
	if Compare("8.10.0", "8.4.16") <= 0 || Compare("8.4.16", "8.4.16") != 0 || Compare("5.0.14.1", "5.0.14") <= 0 {
		t.Fatal("Compare must order dotted numbers numerically")
	}
	r := &Registry{}
	r.Add(Version{Component: PHP, Version: "8.4.16"})
	r.Add(Version{Component: PHP, Version: "8.3.12"})
	r.Add(Version{Component: PHP, Version: "8.4.26"})
	if v, _ := r.Find(PHP, "8.4"); v.Version != "8.4.26" {
		t.Errorf("Find(8.4) = %s, want the newest 8.4.x", v.Version)
	}
	if v, _ := r.Find(PHP, "8.4.16"); v.Version != "8.4.16" {
		t.Errorf("exact versions must match")
	}
	if _, ok := r.Find(PHP, "8"); !ok {
		t.Errorf("a major version must match")
	}
	if _, ok := r.Find(PHP, "8.1"); ok {
		t.Errorf("8.1 is not installed")
	}
	if v, _ := r.Find(PHP, ""); v.Version != "8.4.26" {
		t.Errorf("empty means newest")
	}
}

func TestFrankenPHPVersionLine(t *testing.T) {
	m := frankenRe.FindStringSubmatch("FrankenPHP 1.13.1 PHP 8.5.11 Caddy v2.11.7 h1:yj0Y4fYZGPkSvibBJ1sTWE33xC0fxztVyXEW5iIdUT4=")
	if m == nil || m[1] != "1.13.1" || m[2] != "8.5.11" {
		t.Fatalf("got %v", m)
	}
	v := Version{Component: FrankenPHP, Version: "1.13.1", PHP: "8.5.11", Arch: "x64"}
	if v.Label() != "FrankenPHP 1.13.1 (x64 PHP 8.5.11)" {
		t.Fatalf("label = %q", v.Label())
	}
}
