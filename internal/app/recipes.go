package app

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/sites"
)

// Recipe is a project template of the user's own, config\recetas\<id>.toml: where the project comes
// from, its database, the commands that finish it and values for its .env.
type Recipe struct {
	ID       string            `toml:"-" json:"id"`
	Name     string            `toml:"name" json:"name"`
	Source   RecipeSource      `toml:"source" json:"source"`
	Database string            `toml:"database,omitempty" json:"database,omitempty"`
	Steps    []string          `toml:"steps,omitempty" json:"steps,omitempty"`
	Env      map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	Error    string            `toml:"-" json:"error,omitempty"` // the file could not be read
}

// RecipeSource is where a recipe's project comes from.
type RecipeSource struct {
	Type    string `toml:"type" json:"type"`                           // laravel, wordpress, symfony, composer, git or folder
	Kit     string `toml:"kit,omitempty" json:"kit,omitempty"`         // laravel: react, vue, svelte, livewire
	PM      string `toml:"pm,omitempty" json:"pm,omitempty"`           // laravel: npm, pnpm or yarn
	Locale  string `toml:"locale,omitempty" json:"locale,omitempty"`   // wordpress
	Webapp  bool   `toml:"webapp,omitempty" json:"webapp,omitempty"`   // symfony
	Package string `toml:"package,omitempty" json:"package,omitempty"` // composer: vendor/package
	URL     string `toml:"url,omitempty" json:"url,omitempty"`         // git
	Branch  string `toml:"branch,omitempty" json:"branch,omitempty"`   // git
	Path    string `toml:"path,omitempty" json:"path,omitempty"`       // folder, inside config\plantillas
}

// recipeExample is written to config\recetas the first time, to show the format.
const recipeExample = `# Receta de ejemplo: un proyecto Laravel con el kit de Livewire y el panel de Filament.
# Las recetas son archivos .toml en esta carpeta; aparecen en «Nuevo proyecto → Plantilla»
# y en 'nido new <nombre> --receta <archivo sin .toml>'.
#
# source.type: laravel (kit, pm), wordpress (locale), symfony (webapp), git (url, branch),
#              composer (package, como 'composer create-project') o folder (path, dentro de
#              config\plantillas; lo usa «Guardar como plantilla»).
# database:    sqlite, mysql, mariadb o postgresql (se puede cambiar al crear el proyecto).
# steps:       órdenes que se ejecutan en la carpeta del proyecto, con el php, composer y npm de Nido.
# [env]:       valores para el .env del proyecto (.env.local en Symfony).
name = "Laravel + Filament"
source = { type = "laravel", kit = "livewire" }
database = "mysql"
steps = [
  "composer require filament/filament",
  "php artisan filament:install --panels --no-interaction",
  "php artisan migrate --force",
]

[env]
APP_LOCALE = "es"
`

func (d *Daemon) recipesDir() string   { return d.env.Home.Config("recetas") }
func (d *Daemon) templatesDir() string { return d.env.Home.Config("plantillas") }

// ensureRecipesDir creates config\recetas the first time, with an example that shows the format.
func (d *Daemon) ensureRecipesDir() error {
	dir := d.recipesDir()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "laravel-filament.toml"), []byte(recipeExample), 0o644)
}

// Recipes lists the recipes in config\recetas, writing the example the first time.
func (d *Daemon) Recipes() []Recipe {
	d.ensureRecipesDir()
	dir := d.recipesDir()
	entries, _ := os.ReadDir(dir)
	out := []Recipe{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".toml") {
			continue
		}
		out = append(out, readRecipe(filepath.Join(dir, e.Name())))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func readRecipe(path string) Recipe {
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	r := Recipe{ID: id}
	if _, err := toml.DecodeFile(path, &r); err != nil {
		return Recipe{ID: id, Name: id, Error: err.Error()}
	}
	r.ID = id
	if r.Name == "" {
		r.Name = id
	}
	if err := r.check(); err != nil {
		r.Error = err.Error()
	}
	return r
}

func (r Recipe) check() error {
	switch r.Source.Type {
	case "laravel", "wordpress", "symfony":
	case "composer":
		if r.Source.Package == "" {
			return fmt.Errorf("source.package falta (por ejemplo laravel/laravel)")
		}
	case "git":
		if r.Source.URL == "" {
			return fmt.Errorf("source.url falta")
		}
	case "folder":
		if r.Source.Path == "" || strings.Contains(r.Source.Path, "..") || filepath.IsAbs(r.Source.Path) {
			return fmt.Errorf("source.path debe ser una carpeta dentro de config\\plantillas")
		}
	default:
		return fmt.Errorf("source.type %q no existe: usa laravel, wordpress, symfony, composer, git o folder", r.Source.Type)
	}
	return nil
}

// findRecipe loads config\recetas\<id>.toml.
func (d *Daemon) findRecipe(id string) (Recipe, error) {
	id = strings.TrimSuffix(strings.TrimSpace(id), ".toml")
	if id == "" || strings.ContainsAny(id, `\/:`) {
		return Recipe{}, fmt.Errorf("receta no válida %q", id)
	}
	path := filepath.Join(d.recipesDir(), id+".toml")
	if _, err := os.Stat(path); err != nil {
		return Recipe{}, fmt.Errorf("no hay ninguna receta %s en %s", id, d.recipesDir())
	}
	r := readRecipe(path)
	if r.Error != "" {
		return r, fmt.Errorf("receta %s: %s", id, r.Error)
	}
	return r, nil
}

// fromRecipe fills a new project with what its recipe says (the database chosen when creating it
// wins over the recipe's).
func fromRecipe(p NewProject, r Recipe) NewProject {
	if p.DB == "" {
		p.DB = r.Database
	}
	switch r.Source.Type {
	case "laravel":
		p.Type, p.Kit, p.PM = "laravel", r.Source.Kit, r.Source.PM
	case "wordpress":
		p.Type, p.Locale = "wordpress", r.Source.Locale
	case "symfony":
		p.Type, p.Webapp = "symfony", r.Source.Webapp
	case "git":
		p.Type, p.Git, p.Branch = "laravel", r.Source.URL, r.Source.Branch
	}
	p.recipe = &r
	return p
}

// createFromSource creates the project of a composer or folder recipe: the files, then what any
// existing project gets (Composer packages, .env, key, database, frontend).
func (d *Daemon) createFromSource(p NewProject, name, root, dir, cancelled string) (Job, error) {
	r := p.recipe
	if _, err := os.Stat(install.ComposerPhar(d.env.Home)); err != nil {
		return Job{}, fmt.Errorf("falta Composer: instálalo con 'nido composer install'")
	}
	db := p.DB
	if _, ok := projectDBs[db]; db != "" && !ok {
		return Job{}, fmt.Errorf("base de datos desconocida %q: usa sqlite, mysql, mariadb o postgresql", db)
	}
	return d.startCreateJob(p, name, dir, r.Name, cancelled,
		func(ctx context.Context, phase func(string), log *os.File) (string, error) {
			switch r.Source.Type {
			case "composer":
				global, err := d.env.GlobalPHP()
				if err != nil {
					return "", err
				}
				ini, err := php.WriteIni(d.env.Home, global, d.env.IniOptions())
				if err != nil {
					return "", err
				}
				phase("descargando " + r.Source.Package + " con Composer")
				env := []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH(), "COMPOSER_NO_INTERACTION=1"}
				err = procutil.RunTree(ctx, root, env, log, filepath.Join(global.Path, "php.exe"), "-c", ini,
					install.ComposerPhar(d.env.Home), "create-project", r.Source.Package, dir, "--prefer-dist", "--no-interaction")
				if err != nil {
					return "", fmt.Errorf("composer create-project: %w", err)
				}
			case "folder":
				phase("copiando la plantilla " + r.Source.Path)
				if err := copyTree(filepath.Join(d.templatesDir(), r.Source.Path), dir, nil, nil); err != nil {
					return "", err
				}
			}
			return "", d.prepareProject(ctx, dir, name, db, phase, log)
		})
}

// startCreateJob starts creating a project in dir; with a recipe, its .env values and steps follow.
func (d *Daemon) startCreateJob(p NewProject, name, dir, label, cancelled string, work projectWork) (Job, error) {
	if p.recipe != nil {
		r, base := *p.recipe, work
		label = r.Name
		work = func(ctx context.Context, phase func(string), log *os.File) (string, error) {
			note, err := base(ctx, phase, log)
			if err != nil {
				return note, err
			}
			return note, d.applyRecipe(ctx, r, p.Type, dir, phase, log)
		}
	}
	return d.startProjectJob(name, label, "nuevo-"+name, cancelled, work)
}

// applyRecipe writes a recipe's .env values and runs its steps in the project folder, where php,
// composer, npm… are Nido's (the steps see Nido's shims first in their PATH).
func (d *Daemon) applyRecipe(ctx context.Context, r Recipe, kind, dir string, phase func(string), log *os.File) error {
	if len(r.Env) > 0 {
		envFile := filepath.Join(dir, ".env")
		if kind == "symfony" {
			envFile = filepath.Join(dir, ".env.local")
		}
		var keys []string
		for k := range r.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var values [][2]string
		for _, k := range keys {
			values = append(values, [2]string{k, r.Env[k]})
		}
		if fileExists(envFile) {
			phase("escribiendo los valores de la receta en " + filepath.Base(envFile))
			if err := editEnvFile(envFile, values); err != nil {
				return err
			}
		}
	}
	env := []string{d.env.ChildPATH(), "COMPOSER_NO_INTERACTION=1", "NPM_CONFIG_PREFIX=" + d.env.NPMPrefix()}
	for _, step := range r.Steps {
		if step = strings.TrimSpace(step); step == "" {
			continue
		}
		phase("receta: " + step)
		if err := procutil.RunShell(ctx, dir, env, log, step); err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
	}
	return nil
}

// templateSkip are the parts of a project «Guardar como plantilla» leaves out: what Composer and npm
// rebuild, the local .env (it may hold secrets), Git's history, the compiled frontend and the SQLite
// database.
var templateSkip = map[string]bool{
	"vendor": true, "node_modules": true, ".git": true, ".env": true, ".env.local": true,
	filepath.Join("public", "hot"): true, filepath.Join("public", "build"): true, filepath.Join("public", "storage"): true,
	filepath.Join("database", "database.sqlite"): true,
}

// templateEmpty are copied with only their .gitignore files: Laravel needs these folders, not what
// this computer put in them (logs, caches, sessions, uploads).
var templateEmpty = []string{
	filepath.Join("storage", "logs"), filepath.Join("storage", "framework"), filepath.Join("storage", "app"),
	filepath.Join("bootstrap", "cache"),
}

// SaveTemplate copies a site into config\plantillas\<id> and writes a recipe that creates new
// projects from it, with the database its .env uses.
func (d *Daemon) SaveTemplate(site, name string) (Recipe, error) {
	s, err := d.findSite(site)
	if err != nil {
		return Recipe{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = s.Name
	}
	id, ok := sites.Normalize(name)
	if !ok {
		return Recipe{}, fmt.Errorf("«%s» no sirve como nombre de plantilla", name)
	}
	dest := filepath.Join(d.templatesDir(), id)
	recipePath := filepath.Join(d.recipesDir(), id+".toml")
	if fileExists(dest) || fileExists(recipePath) {
		return Recipe{}, fmt.Errorf("ya hay una plantilla %s: elige otro nombre", id)
	}
	if err := d.ensureRecipesDir(); err != nil {
		return Recipe{}, err
	}
	if err := copyTree(s.Folder, dest, templateSkip, templateEmpty); err != nil {
		os.RemoveAll(dest)
		return Recipe{}, err
	}
	r := Recipe{ID: id, Name: strings.TrimSpace(name), Source: RecipeSource{Type: "folder", Path: id}}
	switch envValueOf(filepath.Join(s.Folder, ".env"), "DB_CONNECTION") {
	case "mysql":
		r.Database = "mysql"
	case "mariadb":
		r.Database = "mariadb"
	case "pgsql":
		r.Database = "postgresql"
	case "sqlite":
		r.Database = "sqlite"
	}
	f, err := os.Create(recipePath)
	if err != nil {
		os.RemoveAll(dest)
		return Recipe{}, err
	}
	fmt.Fprintf(f, "# Plantilla guardada desde %s: los proyectos nuevos copian config\\plantillas\\%s\n", s.Host, id)
	fmt.Fprintln(f, "# y después se preparan (Composer, .env desde .env.example, base de datos, frontend).")
	err = toml.NewEncoder(f).Encode(r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return r, err
}

// copyTree copies a folder; skip lists paths (relative to src) to leave out, and in the folders of
// empty only the .gitignore files are copied. Links and junctions are not followed (Laravel's
// public\storage is one).
func copyTree(src, dst string, skip map[string]bool, empty []string) error {
	return filepath.WalkDir(src, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if skip[rel] {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			return nil
		}
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		for _, dir := range empty {
			if strings.HasPrefix(rel, dir+string(filepath.Separator)) && e.Name() != ".gitignore" {
				return nil
			}
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
