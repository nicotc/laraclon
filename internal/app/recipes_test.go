package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nicotc/laraclon/internal/sites"
)

func TestTheExampleRecipeReadsAsAProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "laravel-filament.toml")
	if err := os.WriteFile(path, []byte(recipeExample), 0o644); err != nil {
		t.Fatal(err)
	}
	r := readRecipe(path)
	if r.Error != "" || r.ID != "laravel-filament" || r.Name != "Laravel + Filament" || r.Source.Type != "laravel" ||
		r.Source.Kit != "livewire" || r.Database != "mysql" || len(r.Steps) != 3 || r.Env["APP_LOCALE"] != "es" {
		t.Fatalf("got %+v", r)
	}
	p := fromRecipe(NewProject{Name: "panel"}, r)
	if p.Type != "laravel" || p.Kit != "livewire" || p.DB != "mysql" || p.recipe == nil {
		t.Fatalf("got %+v", p)
	}
	if p := fromRecipe(NewProject{Name: "panel", DB: "sqlite"}, r); p.DB != "sqlite" {
		t.Fatal("the database chosen when creating the project wins over the recipe's")
	}
}

func TestBrokenRecipesSayWhy(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"sin-tipo.toml":   `name = "x"`,
		"carpeta.toml":    `source = { type = "folder", path = "../fuera" }`,
		"composer.toml":   `source = { type = "composer" }`,
		"no-es-toml.toml": `name = `,
	}
	for file, text := range cases {
		os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644)
		if r := readRecipe(filepath.Join(dir, file)); r.Error == "" {
			t.Errorf("%s should not be usable: %+v", file, r)
		}
	}
}

func TestCopyTreeLeavesOutWhatTemplatesSkip(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "plantilla")
	for _, f := range []string{"artisan", ".env", ".env.example", "app/Models/User.php", "vendor/autoload.php",
		"node_modules/vite/index.js", "storage/logs/laravel.log", "storage/logs/.gitignore", "storage/app/.gitignore",
		"storage/app/public/foto.jpg", "storage/framework/views/.gitignore", "storage/framework/views/abc.php",
		"database/database.sqlite", "database/migrations/0001_create_users_table.php", "public/index.php", "public/build/manifest.json"} {
		p := filepath.Join(src, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(f), 0o644)
	}
	if err := copyTree(src, dst, templateSkip, templateEmpty); err != nil {
		t.Fatal(err)
	}
	var got []string
	filepath.WalkDir(dst, func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(dst, p)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	want := []string{".env.example", "app/Models/User.php", "artisan", "database/migrations/0001_create_users_table.php", "public/index.php",
		"storage/app/.gitignore", "storage/framework/views/.gitignore", "storage/logs/.gitignore"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// On a fresh install config\recetas does not exist yet; «Guardar como plantilla» failed there.
func TestSavingATemplateOnAFreshInstall(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "artisan"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(src, ".env"), []byte("DB_CONNECTION=mysql\n"), 0o644)
	d := &Daemon{env: testEnv(t), sites: []sites.Site{{Name: "demo", Host: "demo.test", Folder: src}}}
	r, err := d.SaveTemplate("demo", "base demo")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "base-demo" || r.Database != "mysql" || !fileExists(filepath.Join(d.templatesDir(), "base-demo", "artisan")) {
		t.Fatalf("got %+v", r)
	}
	if got := readRecipe(filepath.Join(d.recipesDir(), "base-demo.toml")); got.Error != "" || got.Source.Type != "folder" {
		t.Fatalf("the saved recipe reads as %+v", got)
	}
	if !fileExists(filepath.Join(d.recipesDir(), "laravel-filament.toml")) {
		t.Fatal("the example recipe should be written along with the folder")
	}
}
