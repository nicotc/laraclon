package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/laragon"
	"github.com/nicotc/laraclon/internal/mysql"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/postgres"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

// NewProject is what «Nuevo proyecto» creates: a Laravel application, with one of the official
// starter kits or none, a WordPress site, or a project cloned from Git, wired to a database clon runs.
type NewProject struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"` // laravel ("") or wordpress
	Kit  string `json:"kit"`            // "" (none), react, vue, svelte or livewire
	DB   string `json:"db"`             // sqlite, mysql, mariadb or postgresql ("" with Git: the project's own)
	Root string `json:"root"`           // one of the project roots ("" = the first)
	// PM installs and builds the frontend: npm (""), pnpm or yarn, like `laravel new --pnpm`.
	PM string `json:"pm,omitempty"`
	// Locale is WordPress's language ("es_ES" when empty).
	Locale string `json:"locale,omitempty"`
	// Git clones a repository instead (Kit is then ignored), on its default branch or Branch.
	Git    string `json:"git,omitempty"`
	Branch string `json:"branch,omitempty"`
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
	root, dir, err := d.projectDir(p.Root, name)
	if err != nil {
		return Job{}, err
	}
	if _, err := d.env.GlobalPHP(); err != nil {
		return Job{}, err
	}
	cancelled := "creación cancelada (la carpeta a medio crear queda en " + dir + ")"
	p.DB = strings.ToLower(strings.TrimSpace(p.DB))
	switch strings.ToLower(strings.TrimSpace(p.Type)) {
	case "", "laravel":
	case "wordpress", "wp":
		return d.createWordPress(p, name, dir, cancelled)
	default:
		return Job{}, fmt.Errorf("tipo de proyecto desconocido %q: usa laravel o wordpress", p.Type)
	}
	p.Kit = strings.ToLower(strings.TrimSpace(p.Kit))
	if p.Kit == "ninguno" || p.Kit == "none" {
		p.Kit = ""
	}
	if _, ok := starterKits[p.Kit]; !ok {
		return Job{}, fmt.Errorf("kit desconocido %q: usa react, vue, svelte, livewire o ninguno", p.Kit)
	}
	p.Git, p.Branch = strings.TrimSpace(p.Git), strings.TrimSpace(p.Branch)
	switch p.PM = strings.ToLower(strings.TrimSpace(p.PM)); p.PM {
	case "", "npm":
		p.PM = "npm"
	case "pnpm", "yarn":
	default:
		return Job{}, fmt.Errorf("gestor de paquetes desconocido %q: usa npm, pnpm o yarn", p.PM)
	}
	if p.DB == "" && p.Git == "" {
		p.DB = "sqlite"
	}
	if p.DB == "pgsql" {
		p.DB = "postgresql"
	}
	if p.DB != "" {
		db, ok := projectDBs[p.DB]
		if !ok {
			return Job{}, fmt.Errorf("base de datos desconocida %q: usa sqlite, mysql, mariadb o postgresql", p.DB)
		}
		if db.kind != "" {
			if _, ok := d.env.DefaultInstance(db.kind); !ok {
				return Job{}, fmt.Errorf("%s no está instalado: instálalo en Servicios o elige otra base de datos", db.kind.Name())
			}
		}
	}
	var git string
	if p.Git != "" {
		var err error
		if git, err = findGit(); err != nil {
			return Job{}, err
		}
		p.Kit = ""
	}
	if _, err := os.Stat(install.ComposerPhar(d.env.Home)); err != nil {
		return Job{}, fmt.Errorf("falta Composer: instálalo con 'clon composer install'")
	}
	if p.Git == "" {
		if _, err := d.env.GlobalNode(); err != nil {
			return Job{}, fmt.Errorf("hace falta Node para compilar el frontend: %w", err)
		}
	}
	if p.Git != "" {
		return d.startProjectJob(name, "Git", "nuevo-"+name, cancelled,
			func(ctx context.Context, phase func(string), log *os.File) (string, error) {
				phase("clonando " + p.Git)
				args := []string{"clone"}
				if p.Branch != "" {
					args = append(args, "--branch", p.Branch)
				}
				// Without a terminal, Git must fail instead of asking for a user name or password.
				if err := procutil.RunTree(ctx, root, []string{"GIT_TERMINAL_PROMPT=0"}, log, git, append(args, p.Git, dir)...); err != nil {
					return "", fmt.Errorf("git clone: %w", err)
				}
				return "", d.prepareProject(ctx, dir, name, p.DB, phase, log)
			})
	}
	return d.startProjectJob(name, kitLabels[p.Kit], "nuevo-"+name, cancelled,
		func(ctx context.Context, phase func(string), log *os.File) (string, error) {
			return "", d.buildProject(ctx, p, name, dir, phase, log)
		})
}

// projectDir checks where a new project goes: in one of clon's project roots ("" = the first), in a
// folder that does not exist yet.
func (d *Daemon) projectDir(root, name string) (string, string, error) {
	if root == "" && len(d.env.Cfg.Roots) > 0 {
		root = d.env.Cfg.Roots[0]
	}
	known := false
	for _, r := range d.env.Cfg.Roots {
		known = known || strings.EqualFold(filepath.Clean(r), filepath.Clean(root))
	}
	if !known {
		return "", "", fmt.Errorf("%q no es una carpeta de proyectos de clon (roots en clon.toml)", root)
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err == nil {
		return "", "", fmt.Errorf("ya existe %s", dir)
	}
	return root, dir, nil
}

// SetupProject prepares a project that is already in a project folder, cloned or copied: what it
// lacks (Composer packages, .env with clon's databases, key, database, compiled frontend, storage
// link) is done in the background. db chooses the database of a new .env ("" keeps the project's).
func (d *Daemon) SetupProject(site, db string) (Job, error) {
	s, err := d.findSite(site)
	if err != nil {
		return Job{}, err
	}
	db = strings.ToLower(strings.TrimSpace(db))
	if db == "pgsql" {
		db = "postgresql"
	}
	if _, ok := projectDBs[db]; db != "" && !ok {
		return Job{}, fmt.Errorf("base de datos desconocida %q: usa sqlite, mysql, mariadb o postgresql", db)
	}
	if _, err := os.Stat(install.ComposerPhar(d.env.Home)); err != nil {
		return Job{}, fmt.Errorf("falta Composer: instálalo con 'clon composer install'")
	}
	return d.startProjectJob(s.Name, "preparar", "preparar-"+s.Name, "preparación cancelada",
		func(ctx context.Context, phase func(string), log *os.File) (string, error) {
			return "", d.prepareProject(ctx, s.Folder, s.Name, db, phase, log)
		})
}

// projectWork is the work of a project job: phase names each step and the tools write their output
// to log. The note it returns is shown when the job ends (WordPress's generated password, say).
type projectWork func(ctx context.Context, phase func(string), log *os.File) (note string, err error)

// startProjectJob runs work on a project in the background, as a job the panel and the CLI follow
// phase by phase; the tools' output goes to logs\<logName>.log.
func (d *Daemon) startProjectJob(name, label, logName, cancelled string, work projectWork) (Job, error) {
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
	j := &Job{ID: hex.EncodeToString(id), Kind: "project", Series: name, Version: label,
		Phase: "preparando", Started: time.Now(), cancel: cancel}
	d.jobs.jobs[j.ID] = j
	go d.runProjectJob(ctx, j, logName, cancelled, work)
	return *j, nil
}

func (d *Daemon) runProjectJob(ctx context.Context, j *Job, logName, cancelled string, work projectWork) {
	defer j.cancel()
	logPath := d.env.Home.Logs(logName + ".log")
	logf, err := os.Create(logPath)
	note := ""
	if err == nil {
		phase := func(s string) {
			d.jobs.update(j, func(j *Job) { j.Phase = s })
			fmt.Fprintf(logf, "\r\n==== %s · %s ====\r\n", time.Now().Format("15:04:05"), s)
		}
		note, err = work(ctx, phase, logf)
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
			j.Phase, j.Error = "cancelado", cancelled
		case err != nil:
			j.Phase, j.Error = "error", err.Error()
		default:
			j.Phase, j.Note = "listo", note
		}
	})
	d.changed()
}

// buildProject creates a Laravel project with Composer, writing everything the tools print to log.
func (d *Daemon) buildProject(ctx context.Context, p NewProject, name, dir string, phase func(string), log *os.File) error {
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
	if sites.HasVite(dir) {
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

	// Like Laravel's installer, with or without a kit: the pages need the compiled frontend. It also
	// drops the other managers' lock files, points the kit's Composer scripts at the chosen one and
	// leaves out the packages' install scripts.
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		run, err := d.ensureJSRunner(ctx, p.PM, dir, phase, log)
		if err != nil {
			return err
		}
		for _, f := range sites.LockFiles(p.PM) {
			os.Remove(filepath.Join(dir, f))
		}
		if err := useComposerScripts(filepath.Join(dir, "composer.json"), p.PM); err != nil {
			return err
		}
		phase("instalando las dependencias con " + p.PM)
		if err := run.run(ctx, dir, env, log, run.installArgs(true)...); err != nil {
			return fmt.Errorf("%s install: %w", p.PM, err)
		}
		phase("compilando el frontend con Vite")
		if err := run.run(ctx, dir, env, log, "run", "build"); err != nil {
			return fmt.Errorf("%s run build: %w", p.PM, err)
		}
	}
	return nil
}

// prepareProject gives a project that already exists what it lacks to run here: Composer packages, a
// .env from .env.example wired to clon's databases (db chooses one; "" keeps the project's), APP_KEY,
// its database (migrated only when clon created it, so existing data is never touched), the storage
// link and the compiled frontend.
func (d *Daemon) prepareProject(ctx context.Context, dir, name, db string, phase func(string), log *os.File) error {
	v, err := d.env.GlobalPHP()
	if s, ferr := d.findSite(name); ferr == nil {
		v, err = d.sitePHP(s) // a project that pinned its PHP installs with it
	}
	if err != nil {
		return err
	}
	ini, err := php.WriteIni(d.env.Home, v, d.env.IniOptions())
	if err != nil {
		return err
	}
	env := []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH(), "COMPOSER_NO_INTERACTION=1", "NPM_CONFIG_PREFIX=" + d.env.NPMPrefix()}
	runPHP := func(args ...string) error {
		return procutil.RunTree(ctx, dir, env, log, filepath.Join(v.Path, "php.exe"), append([]string{"-c", ini}, args...)...)
	}
	has := func(parts ...string) bool { return fileExists(filepath.Join(append([]string{dir}, parts...)...)) }
	laravel := has("artisan")

	if has("composer.json") && !has("vendor", "autoload.php") {
		phase("instalando los paquetes de Composer")
		if err := runPHP(install.ComposerPhar(d.env.Home), "install", "--no-interaction", "--prefer-dist"); err != nil {
			return fmt.Errorf("composer install: %w", err)
		}
	}

	envFile := filepath.Join(dir, ".env")
	var ref InstanceRef
	dbName, createdDB := "", false
	if laravel && !has(".env") && has(".env.example") {
		phase("creando el .env con las bases de datos de clon")
		data, err := os.ReadFile(filepath.Join(dir, ".env.example"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(envFile, data, 0o644); err != nil {
			return err
		}
		values := [][2]string{{"APP_URL", d.projectURL(name)}}
		conn := envValueOf(envFile, "DB_CONNECTION")
		if db != "" {
			conn = projectDBs[db].connection
		}
		switch conn {
		case "mysql", "mariadb", "pgsql":
			kinds := map[string][]versions.Component{"mysql": {versions.MySQL, versions.MariaDB}, "mariadb": {versions.MariaDB, versions.MySQL}, "pgsql": {versions.PostgreSQL}}[conn]
			ok := false
			for _, k := range kinds {
				if ref, ok = d.env.DefaultInstance(k); ok {
					break
				}
			}
			if !ok {
				return fmt.Errorf("el proyecto usa %s y clon no tiene ninguno instalado: instálalo en Servicios", kinds[0].Name())
			}
			dbName = envValueOf(envFile, "DB_DATABASE")
			if dbName == "" || dbName == "laravel" { // the example's name would be shared by every project
				dbName = strings.ReplaceAll(name, "-", "_")
			}
			user := "root"
			if conn == "pgsql" {
				user = "postgres"
			}
			values = append(values, [2]string{"DB_CONNECTION", conn}, [2]string{"DB_HOST", "127.0.0.1"}, [2]string{"DB_PORT", fmt.Sprint(ref.Instance.Port)},
				[2]string{"DB_DATABASE", dbName}, [2]string{"DB_USERNAME", user}, [2]string{"DB_PASSWORD", ""})
		default:
			values = append(values, [2]string{"DB_CONNECTION", "sqlite"})
			if !has("database", "database.sqlite") && has("database") {
				os.WriteFile(filepath.Join(dir, "database", "database.sqlite"), nil, 0o644)
				createdDB = true
			}
		}
		if mail, ok := d.env.DefaultInstance(versions.Mailpit); ok {
			values = append(values, [2]string{"MAIL_MAILER", "smtp"}, [2]string{"MAIL_HOST", "127.0.0.1"}, [2]string{"MAIL_PORT", fmt.Sprint(mail.Instance.Port)})
		}
		if err := editEnvFile(envFile, values); err != nil {
			return err
		}
	}
	if laravel && has(".env") && envValueOf(envFile, "APP_KEY") == "" {
		phase("generando APP_KEY")
		if err := runPHP("artisan", "key:generate", "--force", "--no-interaction"); err != nil {
			return fmt.Errorf("php artisan key:generate: %w", err)
		}
	}
	if dbName != "" {
		phase(fmt.Sprintf("creando la base de datos %s en %s %s", dbName, ref.Kind.Name(), ref.Version.Version))
		if d.sup.PID(ref.Key()) == 0 {
			if err := d.StartService(ref.Key()); err != nil {
				return err
			}
		}
		exists, err := d.databaseExists(ref, dbName)
		if err != nil {
			return err
		}
		if !exists {
			if err := d.createDatabase(ctx, ref, dbName, log); err != nil {
				return err
			}
			createdDB = true
		}
	}
	if laravel && createdDB {
		phase("migrando la base de datos nueva")
		if err := runPHP("artisan", "migrate", "--force", "--no-interaction"); err != nil {
			return fmt.Errorf("php artisan migrate: %w", err)
		}
	}
	if laravel && has("storage", "app", "public") && !has("public", "storage") {
		phase("enlazando public/storage")
		if err := runPHP("artisan", "storage:link", "--no-interaction"); err != nil {
			return fmt.Errorf("php artisan storage:link: %w", err)
		}
	}
	// With the project's own package manager (its lock file says which), installed first if needed.
	if sites.HasVite(dir) && !has("public", "build", "manifest.json") {
		pm := projectManager(dir)
		if pm != sites.PackageManager(dir) {
			fmt.Fprintf(log, "\r\nclon todavía no instala %s: se usa npm\r\n", sites.PackageManager(dir))
		}
		run, err := d.ensureJSRunner(ctx, pm, dir, phase, log)
		if err != nil {
			return err
		}
		if !has("node_modules") {
			phase("instalando las dependencias con " + pm)
			if err := run.run(ctx, dir, env, log, run.installArgs(false)...); err != nil {
				return fmt.Errorf("%s install: %w", pm, err)
			}
		}
		phase("compilando el frontend con Vite")
		if err := run.run(ctx, dir, env, log, "run", "build"); err != nil {
			return fmt.Errorf("%s run build: %w", pm, err)
		}
	}
	return nil
}

// databaseExists reports whether a database exists in a MySQL, MariaDB or PostgreSQL instance.
func (d *Daemon) databaseExists(ref InstanceRef, name string) (bool, error) {
	port := fmt.Sprint(ref.Instance.Port)
	if ref.Kind == versions.PostgreSQL {
		psql := postgres.Psql(ref.Version)
		if psql == "" {
			return false, fmt.Errorf("PostgreSQL %s no trae psql", ref.Version.Version)
		}
		out, err := procutil.Output(time.Minute, filepath.Dir(psql), psql, "-h", "127.0.0.1", "-p", port, "-U", "postgres", "-tAc",
			"SELECT 1 FROM pg_database WHERE datname = '"+name+"'")
		return strings.TrimSpace(out) == "1", err
	}
	client := mysql.ClientExe(ref.Version)
	if client == "" {
		return false, fmt.Errorf("%s %s no trae el cliente mysql", ref.Kind.Name(), ref.Version.Version)
	}
	out, err := procutil.Output(time.Minute, filepath.Dir(client), client, "--host=127.0.0.1", "--port="+port, "--user=root", "-N",
		"-e", "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '"+name+"'")
	return strings.TrimSpace(lastLine(out)) == "1", err
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// findGit locates git.exe: on the PATH, or the copy Laragon ships.
func findGit() (string, error) {
	if p, err := exec.LookPath("git"); err == nil {
		return p, nil
	}
	for _, c := range []string{
		filepath.Join(laragon.DefaultRoot, "bin", "git", "cmd", "git.exe"),
		filepath.Join(laragon.DefaultRoot, "bin", "git", "bin", "git.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Git", "cmd", "git.exe"),
	} {
		if fileExists(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("para clonar hace falta Git: instálalo desde git-scm.com")
}

// envValueOf reads one key of a .env file ("" when it is missing, empty or commented out).
func envValueOf(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=[ \t]*(.*?)[ \t]*\r?$`).FindStringSubmatch(string(data))
	if m == nil {
		return ""
	}
	return strings.Trim(m[1], `"'`)
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
