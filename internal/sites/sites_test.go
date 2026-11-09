package sites

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nicotc/laraclon/internal/config"
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

func TestHasReverbReadsComposerRequire(t *testing.T) {
	dir := t.TempDir()
	write := func(cj string) {
		if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(cj), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"require": {"laravel/framework": "^13.0"}, "require-dev": {"laravel/reverb": "^1.6"}}`)
	if hasReverb(dir) {
		t.Fatal("only a production requirement runs Reverb")
	}
	write(`{"require": {"laravel/framework": "^13.0", "laravel/reverb": "^1.6"}}`)
	if !hasReverb(dir) {
		t.Fatal("laravel/reverb in require means the project uses Reverb")
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

func TestNodeWantedReadsNvmrcAndNodeVersion(t *testing.T) {
	for files, want := range map[[2]string]string{
		{"v18\n", ""}:         "18",
		{"20.11.1\r\n", ""}:   "20.11.1",
		{"", "22.4"}:          "22.4",
		{"lts/iron\n", "22"}:  "22", // nvm's aliases are left out
		{"\uFEFFv24.1.0", ""}: "24.1.0",
		{"node", ""}:          "",
		{"", ""}:              "",
	} {
		dir := t.TempDir()
		if files[0] != "" {
			os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte(files[0]), 0o644)
		}
		if files[1] != "" {
			os.WriteFile(filepath.Join(dir, ".node-version"), []byte(files[1]), 0o644)
		}
		if got := NodeWanted(dir); got != want {
			t.Errorf("NodeWanted(.nvmrc=%q, .node-version=%q) = %q, quiero %q", files[0], files[1], got, want)
		}
	}
}

func TestWorktreesAreServedAsBranchDotSite(t *testing.T) {
	root, wt := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, "tienda", "public"), 0o755)
	os.WriteFile(filepath.Join(root, "tienda", "artisan"), nil, 0o644)
	os.MkdirAll(filepath.Join(wt, "public"), 0o755)
	os.WriteFile(filepath.Join(wt, "artisan"), nil, 0o644)
	cfg := config.Defaults()
	cfg.Roots = []string{root}
	cfg.Sites = map[string]config.SiteSettings{"tienda": {Server: config.ServerNginx, PHP: "8.3"}}
	cfg.Worktrees = map[string]config.Worktree{
		"feature-pagos.tienda": {Site: "tienda", Branch: "feature/pagos", Folder: wt},
		"vieja.tienda":         {Site: "tienda", Branch: "vieja", Folder: filepath.Join(wt, "no-existe")},
		"x.otro":               {Site: "otro", Branch: "x", Folder: wt},
	}
	list, warns := Scan(cfg)
	if len(list) != 2 {
		t.Fatalf("sites: %+v", list)
	}
	w := list[0]
	if w.Name != "feature-pagos.tienda" || w.Host != "feature-pagos.tienda.test" || w.Worktree != "tienda" || w.Branch != "feature/pagos" {
		t.Fatalf("worktree: %+v", w)
	}
	if w.Server != config.ServerNginx || w.PHP != "8.3" || w.Kind != "laravel" || w.DocRoot != filepath.Join(wt, "public") {
		t.Errorf("the worktree takes the site's settings: %+v", w)
	}
	if len(warns) != 1 || warns[0].Folder != filepath.Join(wt, "no-existe") {
		t.Errorf("warns: %+v", warns)
	}
}
