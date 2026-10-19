package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerHooksComeFromTheKitsComposerJSON(t *testing.T) {
	dir := t.TempDir()
	// Excerpt of laravel/react-starter-kit's composer.json.
	const cj = `{"name": "laravel/react-starter-kit", "extra": {"laravel": {"dont-discover": [],
		"installer": {"post-create-project": ["@php artisan install:features --ansi", ""]}}}}`
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(cj), 0o644); err != nil {
		t.Fatal(err)
	}
	hooks, err := installerHooks(dir)
	if err != nil || len(hooks) != 1 || hooks[0] != "@php artisan install:features --ansi" {
		t.Fatalf("got %q, %v", hooks, err)
	}
	if got := noInteraction([]string{"artisan", "install:features", "--ansi"}); got[len(got)-1] != "--no-interaction" {
		t.Fatalf("got %v", got)
	}
	if got := noInteraction([]string{"artisan", "x", "-n"}); len(got) != 3 {
		t.Fatalf("an existing -n is kept alone, got %v", got)
	}
	// Plain Laravel declares no hooks.
	os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"name": "laravel/laravel"}`), 0o644)
	if hooks, err := installerHooks(dir); err != nil || len(hooks) != 0 {
		t.Fatalf("got %q, %v", hooks, err)
	}
}

func TestUseComposerScriptsSwitchesTheKitsScriptsToPnpm(t *testing.T) {
	// Excerpt of laravel/react-starter-kit's composer.json (October 2026).
	const cj = `{
    "name": "laravel/react-starter-kit",
    "minimum-stability": "dev",
    "scripts": {
        "setup": [
            "composer install",
            "@php -r \"file_exists('.env') || copy('.env.example', '.env');\"",
            "npm install",
            "npm run build"
        ],
        "dev": [
            "Composer\\Config::disableProcessTimeout",
            "npx concurrently -c \"#93c5fd,#c4b5fd\" \"php artisan serve\" \"npm run dev\" --names=server,vite"
        ],
        "dev:ssr": "npm run build:ssr",
        "lint": ["pint --parallel"],
        "ci:check": ["npm run check", "@test"]
    },
    "extra": {"laravel": {"installer": {"post-create-project": ["npm run x"]}}}
}`
	path := filepath.Join(t.TempDir(), "composer.json")
	if err := os.WriteFile(path, []byte(cj), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := useComposerScripts(path, "pnpm"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, want := range []string{
		`"pnpm install",`, `"pnpm run build"`, `"pnpm dlx concurrently -c \"#93c5fd,#c4b5fd\" \"php artisan serve\" \"pnpm run dev\" --names=server,vite"`,
		`"dev:ssr": "pnpm run build:ssr"`, `"ci:check": ["pnpm run check", "@test"]`, `"lint": ["pint --parallel"]`,
		`"post-create-project": ["npm run x"]`, `"minimum-stability": "dev"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %s en:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ppnpm") {
		t.Errorf("pnpm no se duplica:\n%s", got)
	}
	// npm leaves the file as it was.
	os.WriteFile(path, []byte(cj), 0o644)
	useComposerScripts(path, "npm")
	if data, _ := os.ReadFile(path); string(data) != cj {
		t.Errorf("con npm no cambia nada")
	}
	if got := jsonEnd(`["a]", "b\"]"] tail`, 0); got != 14 {
		t.Errorf("jsonEnd skips brackets inside strings, got %d", got)
	}
}

func TestYarnBerryGoesThroughCorepack(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]bool{
		`{"packageManager": "yarn@4.9.2+sha224.953c8233f7a92884eee2de69a1b92d1f2ec1655e66d08071ba9a02fa"}`: true,
		`{"packageManager": "yarn@10.0.0"}`:  true,
		`{"packageManager": "yarn@1.22.22"}`: false,
		`{"packageManager": "pnpm@10.0.0"}`:  false,
		`{"private": true}`:                  false,
	}
	for pkg, want := range cases {
		os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644)
		if got := yarnBerry(dir); got != want {
			t.Errorf("%s: got %v, want %v", pkg, got, want)
		}
	}
}

func TestEnvValueOfReadsOneKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("APP_NAME=\"Mi App\"\r\nAPP_KEY=\r\n# DB_DATABASE=comentada\r\nDB_CONNECTION=mysql\r\nDB_DATABASE = tienda \r\n"), 0o644)
	cases := map[string]string{"APP_NAME": "Mi App", "APP_KEY": "", "DB_CONNECTION": "mysql", "DB_DATABASE": "tienda", "NO_ESTA": ""}
	for key, want := range cases {
		if got := envValueOf(path, key); got != want {
			t.Errorf("%s: got %q, want %q", key, got, want)
		}
	}
}

func TestEditEnvFileFillsLaravelsEnv(t *testing.T) {
	// Excerpt of a fresh Laravel 13 .env: SQLite by default, the MySQL lines commented out.
	const fresh = "APP_NAME=Laravel\nAPP_ENV=local\nAPP_URL=http://localhost\n\nDB_CONNECTION=sqlite\n# DB_HOST=127.0.0.1\n# DB_PORT=3306\n" +
		"# DB_DATABASE=laravel\n# DB_USERNAME=root\n# DB_PASSWORD=\n\nMAIL_MAILER=log\nMAIL_HOST=127.0.0.1\nMAIL_PORT=2525\n"
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	err := editEnvFile(path, [][2]string{
		{"APP_NAME", "Mi Tienda"}, {"APP_URL", "https://mi-tienda.test"}, {"DB_CONNECTION", "mysql"},
		{"DB_HOST", "127.0.0.1"}, {"DB_PORT", "3307"}, {"DB_DATABASE", "mi_tienda"}, {"DB_USERNAME", "root"}, {"DB_PASSWORD", ""},
		{"MAIL_MAILER", "smtp"}, {"MAIL_PORT", "1025"}, {"REDIS_CLIENT", "predis"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, want := range []string{
		`APP_NAME="Mi Tienda"`, "APP_URL=https://mi-tienda.test", "DB_CONNECTION=mysql", "\nDB_HOST=127.0.0.1\n", "\nDB_PORT=3307\n",
		"\nDB_DATABASE=mi_tienda\n", "\nDB_USERNAME=root\n", "\nDB_PASSWORD=\n", "MAIL_MAILER=smtp", "MAIL_PORT=1025", "REDIS_CLIENT=predis",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en:\n%s", want, got)
		}
	}
	if strings.Contains(got, "# DB_") || strings.Count(got, "DB_PORT=") != 1 {
		t.Errorf("las líneas comentadas se reemplazan, no se duplican:\n%s", got)
	}
	if !strings.Contains(got, "APP_ENV=local") || !strings.Contains(got, "MAIL_HOST=127.0.0.1") {
		t.Errorf("lo demás queda igual:\n%s", got)
	}
}
