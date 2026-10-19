package sites

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingListsWhatAClonedProjectLacks(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"artisan", "composer.json", ".env.example", "package.json", "vite.config.js"} {
		os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644)
	}
	got := Missing(dir, "laravel")
	if len(got) != 3 || got[0] != "vendor" || got[1] != ".env" || got[2] != "frontend" {
		t.Fatalf("a fresh clone lacks vendor, .env and frontend, got %v", got)
	}
	os.MkdirAll(filepath.Join(dir, "vendor"), 0o755)
	os.WriteFile(filepath.Join(dir, "vendor", "autoload.php"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), nil, 0o644)
	os.MkdirAll(filepath.Join(dir, "public", "build"), 0o755)
	os.WriteFile(filepath.Join(dir, "public", "build", "manifest.json"), nil, 0o644)
	if got := Missing(dir, "laravel"); len(got) != 0 {
		t.Fatalf("a prepared project lacks nothing, got %v", got)
	}
}

func TestHasViteNeedsPackageJSONAndAViteConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json")
	if hasVite(dir) {
		t.Fatal("package.json alone is not a Vite project")
	}
	write("vite.config.ts")
	if !hasVite(dir) {
		t.Fatal("package.json and vite.config.ts make a Vite project")
	}
}

func TestPackageManagerFollowsPackageJSONThenTheLockFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := PackageManager(dir); got != "npm" {
		t.Fatalf("without anything the project uses npm, got %s", got)
	}
	write("package.json", `{"private": true}`)
	write("package-lock.json", "{}")
	if got := PackageManager(dir); got != "npm" {
		t.Fatalf("package-lock.json means npm, got %s", got)
	}
	write("yarn.lock", "")
	if got := PackageManager(dir); got != "yarn" {
		t.Fatalf("yarn.lock wins over package-lock.json, got %s", got)
	}
	write("package.json", `{"packageManager": "pnpm@10.11.0+sha512.abc"}`)
	if got := PackageManager(dir); got != "pnpm" {
		t.Fatalf("packageManager in package.json wins over the lock files, got %s", got)
	}
	if got := LockFiles("pnpm"); len(got) != 4 || got[0] != "yarn.lock" {
		t.Fatalf("switching to pnpm drops the other four lock files, got %v", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"app":            "app",
		"tienda_nueva":   "tienda-nueva",
		"mi proyecto  2": "mi-proyecto-2",
		"proyecto final": "proyecto-final",
		"_borrador":      "borrador",
		"2026_campaña":   "2026-campana",
		"MiBlog":         "miblog",
		"araña":          "arana",
		"Diseño Él":      "diseno-el",
		"blog - copia":   "blog-copia",
		"api.v2":         "api-v2",
	}
	for in, want := range cases {
		got, ok := Normalize(in)
		if !ok || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := Normalize("___"); ok {
		t.Error("a name without letters or digits must be rejected")
	}
}
