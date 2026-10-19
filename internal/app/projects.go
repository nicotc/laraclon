package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/mysql"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/postgres"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

// NewProject is what «Nuevo proyecto» creates: a Laravel application, with one of the official
// starter kits or none, wired to a database clon runs.
type NewProject struct {
	Name string `json:"name"`
	Kit  string `json:"kit"`  // "" (none), react, vue, svelte or livewire
	DB   string `json:"db"`   // sqlite, mysql, mariadb or postgresql
	Root string `json:"root"` // one of the project roots ("" = the first)
}

// starterKits are the Composer packages of the official kits; like Laravel's installer, the kits are
// taken from their main branch (--stability=dev), because they publish no tagged releases.
var starterKits = map[string]string{
	"":         "laravel/laravel",
	"react":    "laravel/react-starter-kit",
	"vue":      "laravel/vue-starter-kit",
	"svelte":   "laravel/svelte-starter-kit",
	"livewire": "laravel/livewire-starter-kit",
}

var kitLabels = map[string]string{"": "Laravel", "react": "Laravel + React", "vue": "Laravel + Vue", "svelte": "Laravel + Svelte", "livewire": "Laravel + Livewire"}

// projectDBs are the databases a new project can use, and the Laravel connection of each.
var projectDBs = map[string]struct {
	kind       versions.Component
	connection string
	user       string
}{
	"sqlite":     {"", "sqlite", ""},
	"mysql":      {versions.MySQL, "mysql", "root"},
	"mariadb":    {versions.MariaDB, "mariadb", "root"},
	"postgresql": {versions.PostgreSQL, "pgsql", "postgres"},
}

// CreateProject checks a request and starts creating the project in the background; the panel and
// 'clon new' follow it like an installation (Jobs).
func (d *Daemon) CreateProject(p NewProject) (Job, error) {
	name, ok := sites.Normalize(p.Name)
	if !ok {
		return Job{}, fmt.Errorf("«%s» no sirve como nombre: usa letras, números y guiones", p.Name)
	}
	p.Kit = strings.ToLower(strings.TrimSpace(p.Kit))
	if p.Kit == "ninguno" || p.Kit == "none" {
		p.Kit = ""
	}
	if _, ok := starterKits[p.Kit]; !ok {
		return Job{}, fmt.Errorf("kit desconocido %q: usa react, vue, svelte, livewire o ninguno", p.Kit)
	}
	p.DB = strings.ToLower(strings.TrimSpace(p.DB))
	if p.DB == "" {
		p.DB = "sqlite"
	}
	if p.DB == "pgsql" {
		p.DB = "postgresql"
	}
	db, ok := projectDBs[p.DB]
	if !ok {
		return Job{}, fmt.Errorf("base de datos desconocida %q: usa sqlite, mysql, mariadb o postgresql", p.DB)
	}
	if db.kind != "" {
		if _, ok := d.env.DefaultInstance(db.kind); !ok {
			return Job{}, fmt.Errorf("%s no está instalado: instálalo en Servicios o elige otra base de datos", db.kind.Name())
		}
	}
	root := p.Root
	if root == "" && len(d.env.Cfg.Roots) > 0 {
		root = d.env.Cfg.Roots[0]
	}
	known := false
	for _, r := range d.env.Cfg.Roots {
		known = known || strings.EqualFold(filepath.Clean(r), filepath.Clean(root))
	}
	if !known {
		return Job{}, fmt.Errorf("%q no es una carpeta de proyectos de clon (roots en clon.toml)", root)
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err == nil {
		return Job{}, fmt.Errorf("ya existe %s", dir)
	}
	if _, err := d.env.GlobalPHP(); err != nil {
		return Job{}, err
	}
	if _, err := os.Stat(install.ComposerPhar(d.env.Home)); err != nil {
		return Job{}, fmt.Errorf("falta Composer: instálalo con 'clon composer install'")
	}
	if p.Kit != "" {
		if _, err := d.env.GlobalNode(); err != nil {
			return Job{}, fmt.Errorf("los kits necesitan Node: %w", err)
		}
	}

	d.jobs.mu.Lock()
	defer d.jobs.mu.Unlock()
	for _, j := range d.jobs.jobs {
		if !j.Finished && j.Kind == "project" && j.Series == name {
			return *j, nil
		}
	}
	id := make([]byte, 6)
	rand.Read(id)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	j := &Job{ID: hex.EncodeToString(id), Kind: "project", Series: name, Version: kitLabels[p.Kit],
		Phase: "preparando", Started: time.Now(), cancel: cancel}
	d.jobs.jobs[j.ID] = j
	go d.runProject(ctx, j, p, name, dir)
	return *j, nil
}

func (d *Daemon) runProject(ctx context.Context, j *Job, p NewProject, name, dir string) {
	defer j.cancel()
	logPath := d.env.Home.Logs("nuevo-" + name + ".log")
	logf, err := os.Create(logPath)
	if err == nil {
		err = d.buildProject(ctx, j, p, name, dir, logf)
		logf.Close()
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		data, _ := os.ReadFile(logPath)
		if tail := procutil.LastLines(string(data), 4); tail != "" {
			err = fmt.Errorf("%w\n%s\n(registro completo: %s)", err, tail, logPath)
		}
	}
	// The site is published before the job ends, so whoever follows it can open it at once.
	if err == nil {
		if rerr := d.Rescan(); rerr != nil {
			d.setProblem("sitios", rerr)
		}
	}
	d.jobs.update(j, func(j *Job) {
		j.Finished, j.Ended = true, time.Now()
		switch {
		case errors.Is(err, context.Canceled):
			j.Phase, j.Error = "cancelado", "creación cancelada (la carpeta a medio crear queda en "+dir+")"
		case err != nil:
			j.Phase, j.Error = "error", err.Error()
		default:
			j.Phase = "listo"
		}
	})
	d.changed()
}

// buildProject runs each step, writing everything the tools print to log.
func (d *Daemon) buildProject(ctx context.Context, j *Job, p NewProject, name, dir string, log *os.File) error {
	phase := func(s string) {
		d.jobs.update(j, func(j *Job) { j.Phase = s })
		fmt.Fprintf(log, "\r\n==== %s · %s ====\r\n", time.Now().Format("15:04:05"), s)
	}
	global, err := d.env.GlobalPHP()
	if err != nil {
		return err
	}
	ini, err := php.WriteIni(d.env.Home, global, d.env.IniOptions())
	if err != nil {
		return err
	}
	phpExe := filepath.Join(global.Path, "php.exe")
	env := []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH(), "COMPOSER_NO_INTERACTION=1", "NPM_CONFIG_PREFIX=" + d.env.NPMPrefix()}
	runPHP := func(dir string, args ...string) error {
		return procutil.RunTree(ctx, dir, env, log, phpExe, append([]string{"-c", ini}, args...)...)
	}

	phase("descargando " + kitLabels[p.Kit] + " con Composer")
	args := []string{install.ComposerPhar(d.env.Home), "create-project", starterKits[p.Kit], dir, "--prefer-dist", "--no-interaction"}
	if p.Kit != "" {
		args = append(args, "--stability=dev")
	}
	// Like Laravel's installer: the kits' own setup waits until Composer has finished.
	deferred := append(append([]string(nil), env...), "LARAVEL_INSTALLER_DEFER_HOOKS=1")
	if err := procutil.RunTree(ctx, filepath.Dir(dir), deferred, log, phpExe, append([]string{"-c", ini}, args...)...); err != nil {
		return fmt.Errorf("composer create-project: %w", err)
	}
	hooks, err := installerHooks(dir)
	if err != nil {
		return err
	}
	for _, h := range hooks {
		phase("preparando el kit (" + h + ")")
		var err error
		// On Windows, Artisan and Composer ask their questions unless told not to, and nobody
		// would answer them here; Laravel's installer adds the same option.
		switch {
		case strings.HasPrefix(h, "@php "):
			err = runPHP(dir, noInteraction(strings.Fields(strings.TrimPrefix(h, "@php ")))...)
		case strings.HasPrefix(h, "@composer "):
			err = runPHP(dir, append([]string{install.ComposerPhar(d.env.Home)}, noInteraction(strings.Fields(strings.TrimPrefix(h, "@composer ")))...)...)
		default:
			err = procutil.RunTree(ctx, dir, env, log, filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/s", "/c", h)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", h, err)
		}
	}

	phase("configurando .env")
	db := projectDBs[p.DB]
	values := [][2]string{{"APP_NAME", strings.TrimSpace(p.Name)}, {"APP_URL", d.projectURL(name)}, {"DB_CONNECTION", db.connection}}
	dbName := strings.ReplaceAll(name, "-", "_")
	var ref InstanceRef
	if db.kind != "" {
		ref, _ = d.env.DefaultInstance(db.kind)
		values = append(values, [2]string{"DB_HOST", "127.0.0.1"}, [2]string{"DB_PORT", fmt.Sprint(ref.Instance.Port)},
			[2]string{"DB_DATABASE", dbName}, [2]string{"DB_USERNAME", db.user}, [2]string{"DB_PASSWORD", ""})
	}
	if mail, ok := d.env.DefaultInstance(versions.Mailpit); ok {
		values = append(values, [2]string{"MAIL_MAILER", "smtp"}, [2]string{"MAIL_HOST", "127.0.0.1"}, [2]string{"MAIL_PORT", fmt.Sprint(mail.Instance.Port)})
	}
	// `npm run dev` serves HTTPS with the site's certificate (laravel-vite-plugin reads these).
	if p.Kit != "" {
		if vite, err := d.viteEnv(sites.Site{Host: name + "." + d.env.Cfg.TLD}); err == nil {
			for _, kv := range vite {
				k, v, _ := strings.Cut(kv, "=")
				values = append(values, [2]string{k, v})
			}
		}
	}
	if err := editEnvFile(filepath.Join(dir, ".env"), values); err != nil {
		return err
	}

	if db.kind != "" {
		phase(fmt.Sprintf("creando la base de datos %s en %s %s", dbName, db.kind.Name(), ref.Version.Version))
		if d.sup.PID(ref.Key()) == 0 {
			if err := d.StartService(ref.Key()); err != nil {
				return err
			}
		}
		if err := d.createDatabase(ctx, ref, dbName, log); err != nil {
			return err
		}
	}
	phase("migrando la base de datos")
	if err := runPHP(dir, "artisan", "migrate", "--force"); err != nil {
		return fmt.Errorf("php artisan migrate: %w", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil && p.Kit != "" {
		node, err := d.env.GlobalNode()
		if err != nil {
			return err
		}
		npm := func(args ...string) error {
			cli := filepath.Join(node.Path, "node_modules", "npm", "bin", "npm-cli.js")
			return procutil.RunTree(ctx, dir, env, log, filepath.Join(node.Path, "node.exe"), append([]string{cli}, args...)...)
		}
		phase("instalando las dependencias de npm")
		if err := npm("install", "--no-audit", "--no-fund"); err != nil {
			return fmt.Errorf("npm install: %w", err)
		}
		phase("compilando el frontend con Vite")
		if err := npm("run", "build"); err != nil {
			return fmt.Errorf("npm run build: %w", err)
		}
	}
	return nil
}

// projectURL is the address of a site by name.
func (d *Daemon) projectURL(name string) string {
	return strings.TrimSuffix(d.SiteURL(sites.Site{Host: name + "." + d.env.Cfg.TLD, HTTPS: true}), "/")
}

// createDatabase creates a database in a MySQL, MariaDB or PostgreSQL instance unless it exists.
func (d *Daemon) createDatabase(ctx context.Context, ref InstanceRef, name string, log *os.File) error {
	port := fmt.Sprint(ref.Instance.Port)
	switch ref.Kind {
	case versions.PostgreSQL:
		psql := postgres.Psql(ref.Version)
		if psql == "" {
			return fmt.Errorf("PostgreSQL %s no trae psql", ref.Version.Version)
		}
		out, err := procutil.Output(time.Minute, filepath.Dir(psql), psql, "-h", "127.0.0.1", "-p", port, "-U", "postgres", "-tAc",
			"SELECT 1 FROM pg_database WHERE datname = '"+name+"'")
		if err != nil {
			return fmt.Errorf("psql: %w", err)
		}
		if strings.TrimSpace(out) == "1" {
			return nil
		}
		return procutil.RunTree(ctx, filepath.Dir(psql), nil, log, psql, "-h", "127.0.0.1", "-p", port, "-U", "postgres", "-c", `CREATE DATABASE "`+name+`"`)
	default:
		client := mysql.ClientExe(ref.Version)
		if client == "" {
			return fmt.Errorf("%s %s no trae el cliente mysql", ref.Kind.Name(), ref.Version.Version)
		}
		return procutil.RunTree(ctx, filepath.Dir(client), nil, log, client, "--host=127.0.0.1", "--port="+port, "--user=root",
			"-e", "CREATE DATABASE IF NOT EXISTS `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	}
}

// noInteraction adds --no-interaction to an Artisan or Composer command that lacks it.
func noInteraction(args []string) []string {
	for _, a := range args {
		if a == "--no-interaction" || a == "-n" {
			return args
		}
	}
	return append(args, "--no-interaction")
}

// installerHooks are the commands a starter kit asks Laravel's installer to run once the project
// exists (extra.laravel.installer.post-create-project in its composer.json).
func installerHooks(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return nil, err
	}
	var composer struct {
		Extra struct {
			Laravel struct {
				Installer map[string][]string `json:"installer"`
			} `json:"laravel"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(data, &composer); err != nil {
		return nil, fmt.Errorf("composer.json del proyecto: %w", err)
	}
	var out []string
	for _, c := range composer.Extra.Laravel.Installer["post-create-project"] {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

// editEnvFile sets keys in a .env file, uncommenting the lines Laravel ships commented
// ("# DB_HOST=127.0.0.1") and appending the keys it lacks.
func editEnvFile(path string, values [][2]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("el proyecto no tiene .env: %w", err)
	}
	text := string(data)
	for _, kv := range values {
		line := kv[0] + "=" + envValue(kv[1])
		re := regexp.MustCompile(`(?m)^#?[ \t]*` + regexp.QuoteMeta(kv[0]) + `=.*$`)
		if loc := re.FindStringIndex(text); loc != nil {
			text = text[:loc[0]] + line + text[loc[1]:]
		} else {
			text = strings.TrimRight(text, "\r\n") + "\n" + line + "\n"
		}
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// envValue quotes values with spaces or quotes, as .env files expect.
func envValue(v string) string {
	if strings.ContainsAny(v, " \t#\"'") {
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return v
}
