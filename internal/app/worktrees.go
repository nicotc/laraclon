package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/mysql"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/postgres"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

// Database modes of a new worktree.
const (
	WorktreeDBShared = "compartida" // the site's own database
	WorktreeDBCopy   = "copia"      // a copy of it, data included
	WorktreeDBFresh  = "nueva"      // an empty one, migrated
)

// WorktreeName is the site name of a branch's worktree: the branch as a host label, a dot and the
// site ("feature/Pagos" of tienda → "feature-pagos.tienda", served at feature-pagos.tienda.test).
func WorktreeName(branch, site string) (string, error) {
	label, ok := sites.Normalize(branch)
	if !ok {
		return "", fmt.Errorf("la rama %q no se puede convertir en nombre de dominio", branch)
	}
	return label + "." + site, nil
}

// WorktreeInfo is one worktree as the panel and 'nido worktree list' show it.
type WorktreeInfo struct {
	Name   string `json:"name"`
	Host   string `json:"host"`
	Site   string `json:"site"`
	Branch string `json:"branch"`
	Folder string `json:"folder"`
	DB     string `json:"db,omitempty"`
}

// Worktrees lists the worktrees of a site ("" for every site).
func (d *Daemon) Worktrees(site string) []WorktreeInfo {
	site = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(site)), "."+d.env.Cfg.TLD)
	out := []WorktreeInfo{}
	for name, wt := range d.env.Cfg.Worktrees {
		if site == "" || wt.Site == site {
			out = append(out, WorktreeInfo{Name: name, Host: name + "." + d.env.Cfg.TLD, Site: wt.Site, Branch: wt.Branch, Folder: wt.Folder, DB: wt.DB})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AddWorktree creates a git worktree of a site for a branch (new or existing, local or remote) and
// serves it at <branch>.<site>.test: its own .env with a new APP_KEY, its Composer packages and
// frontend, and a database shared with the site, copied from it or new.
func (d *Daemon) AddWorktree(site, branch, dbMode string) (Job, error) {
	s, err := d.findSite(site)
	if err != nil {
		return Job{}, err
	}
	if s.Worktree != "" {
		return Job{}, fmt.Errorf("%s ya es un worktree de %s.%s: crea el nuevo desde el sitio original", s.Host, s.Worktree, d.env.Cfg.TLD)
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, " \t~^:?*[\\") {
		return Job{}, fmt.Errorf("«%s» no sirve como nombre de rama", branch)
	}
	switch dbMode = strings.ToLower(strings.TrimSpace(dbMode)); dbMode {
	case "":
		dbMode = WorktreeDBShared
	case WorktreeDBShared, WorktreeDBCopy, WorktreeDBFresh:
	case "shared":
		dbMode = WorktreeDBShared
	case "copy":
		dbMode = WorktreeDBCopy
	case "fresh", "new":
		dbMode = WorktreeDBFresh
	default:
		return Job{}, fmt.Errorf("base de datos %q: usa compartida, copia o nueva", dbMode)
	}
	if !fileExists(filepath.Join(s.Folder, ".git")) {
		return Job{}, fmt.Errorf("%s no es un repositorio Git (no tiene .git)", s.Folder)
	}
	name, err := WorktreeName(branch, s.Name)
	if err != nil {
		return Job{}, err
	}
	if _, ok := d.env.Cfg.Worktrees[name]; ok {
		return Job{}, fmt.Errorf("ya hay un worktree %s.%s", name, d.env.Cfg.TLD)
	}
	label, _, _ := strings.Cut(name, ".")
	folder := d.env.Home.Path("worktrees", s.Name, label)
	if _, err := os.Stat(folder); err == nil {
		return Job{}, fmt.Errorf("ya existe %s", folder)
	}
	return d.startProjectJob(name, "worktree", "worktree-"+name, "worktree cancelado",
		func(ctx context.Context, phase func(string), log *os.File) (string, error) {
			return d.buildWorktree(ctx, s, branch, name, folder, dbMode, phase, log)
		})
}

func (d *Daemon) buildWorktree(ctx context.Context, s sites.Site, branch, name, folder, dbMode string, phase func(string), log *os.File) (string, error) {
	git := findGit(d.env.Home)
	if git == "" {
		phase("descargando Git (MinGit)")
		p, err := install.InstallGit(ctx, d.env.Home, nil)
		if err != nil {
			return "", err
		}
		git = p
	}
	if err := os.MkdirAll(filepath.Dir(folder), 0o755); err != nil {
		return "", err
	}
	phase("creando el worktree de la rama " + branch)
	// An existing branch (here or on the remote) is checked out; otherwise it starts from HEAD.
	if err := procutil.RunTree(ctx, s.Folder, nil, log, git, "worktree", "add", folder, branch); err != nil {
		fmt.Fprintf(log, "\r\nLa rama %s no existe: se crea desde la rama actual\r\n", branch)
		if err := procutil.RunTree(ctx, s.Folder, nil, log, git, "worktree", "add", "-b", branch, folder); err != nil {
			return "", fmt.Errorf("git worktree add: %w", err)
		}
	}
	host := name + "." + d.env.Cfg.TLD
	note := ""
	if s.Kind == "laravel" {
		dbNote, err := d.worktreeEnv(ctx, s, name, host, folder, dbMode, phase, log)
		if err != nil {
			return "", err
		}
		note = dbNote
	}
	d.cfgMu.Lock()
	if d.env.Cfg.Worktrees == nil {
		d.env.Cfg.Worktrees = map[string]config.Worktree{}
	}
	wt := config.Worktree{Site: s.Name, Branch: branch, Folder: folder}
	if dbMode != WorktreeDBShared {
		wt.DB = envValueOf(filepath.Join(folder, ".env"), "DB_DATABASE")
	}
	d.env.Cfg.Worktrees[name] = wt
	err := d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return "", err
	}
	if err := d.Rescan(); err != nil {
		return "", err
	}
	if err := d.prepareProject(ctx, folder, name, "", phase, log); err != nil {
		return "", err
	}
	if dbMode == WorktreeDBFresh && s.Kind == "laravel" {
		phase("migrando la base de datos nueva")
		if err := d.artisanIn(ctx, folder, name, log, "migrate", "--force", "--no-interaction"); err != nil {
			return "", fmt.Errorf("php artisan migrate: %w", err)
		}
	}
	return strings.TrimSpace(fmt.Sprintf("Listo en https://%s/. %s", host, note)), nil
}

// worktreeEnv gives the worktree the site's .env with its own address and key, and its database.
func (d *Daemon) worktreeEnv(ctx context.Context, s sites.Site, name, host, folder, dbMode string, phase func(string), log *os.File) (string, error) {
	src, dst := filepath.Join(s.Folder, ".env"), filepath.Join(folder, ".env")
	if !fileExists(src) || fileExists(dst) {
		return "", nil // prepareProject writes one from .env.example
	}
	phase("copiando el .env del sitio")
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	values := [][2]string{{"APP_URL", "https://" + host}, {"APP_KEY", ""}} // prepareProject generates a new key
	if sites.HasVite(folder) {
		if vite, err := d.viteEnv(sites.Site{Host: host}); err == nil {
			for _, kv := range vite {
				k, v, _ := strings.Cut(kv, "=")
				values = append(values, [2]string{k, v})
			}
		}
	}
	note := "Comparte la base de datos de " + s.Host + "."
	if dbMode != WorktreeDBShared {
		dbValues, dbNote, err := d.worktreeDB(ctx, s, name, folder, dbMode, phase, log)
		if err != nil {
			return "", err
		}
		values, note = append(values, dbValues...), dbNote
	}
	return note, editEnvFile(dst, values)
}

// worktreeDB makes the worktree's own database: SQLite's file, or a database next to the site's in
// the same MySQL, MariaDB or PostgreSQL, copied with its data or empty.
func (d *Daemon) worktreeDB(ctx context.Context, s sites.Site, name, folder, dbMode string, phase func(string), log *os.File) ([][2]string, string, error) {
	env := filepath.Join(s.Folder, ".env")
	conn := envValueOf(env, "DB_CONNECTION")
	label, _, _ := strings.Cut(name, ".")
	switch conn {
	case "", "sqlite":
		srcDB := envValueOf(env, "DB_DATABASE")
		if srcDB == "" || !filepath.IsAbs(srcDB) {
			srcDB = filepath.Join(s.Folder, "database", "database.sqlite")
		}
		dstDB := filepath.Join(folder, "database", "database.sqlite")
		if err := os.MkdirAll(filepath.Dir(dstDB), 0o755); err != nil {
			return nil, "", err
		}
		if dbMode == WorktreeDBCopy && fileExists(srcDB) {
			phase("copiando la base de datos SQLite")
			if err := copyFile(srcDB, dstDB); err != nil {
				return nil, "", err
			}
		} else if err := os.WriteFile(dstDB, nil, 0o644); err != nil {
			return nil, "", err
		}
		return [][2]string{{"DB_CONNECTION", "sqlite"}, {"DB_DATABASE", dstDB}}, "Tiene su propia base de datos SQLite.", nil
	case "mysql", "mariadb", "pgsql":
	default:
		return nil, "", fmt.Errorf("el sitio usa la base de datos %q: el worktree solo puede compartirla", conn)
	}
	ref, err := d.instanceFor(conn, envValueOf(env, "DB_PORT"))
	if err != nil {
		return nil, "", err
	}
	srcName := envValueOf(env, "DB_DATABASE")
	if srcName == "" {
		srcName = strings.ReplaceAll(s.Name, "-", "_")
	}
	newName := strings.ReplaceAll(srcName+"_"+label, "-", "_")
	if len(newName) > 63 {
		newName = newName[:63]
	}
	if !dbNameRe.MatchString(newName) {
		return nil, "", fmt.Errorf("«%s» no sirve como nombre de base de datos", newName)
	}
	if d.sup.PID(ref.Key()) == 0 {
		if err := d.StartService(ref.Key()); err != nil {
			return nil, "", err
		}
	}
	if exists, err := d.databaseExists(ref, newName); err != nil {
		return nil, "", err
	} else if exists {
		return nil, "", fmt.Errorf("ya existe la base de datos %s en %s %s", newName, ref.Kind.Name(), ref.Version.Version)
	}
	phase(fmt.Sprintf("creando la base de datos %s en %s %s", newName, ref.Kind.Name(), ref.Version.Version))
	if err := d.createDatabase(ctx, ref, newName, log); err != nil {
		return nil, "", err
	}
	values := [][2]string{{"DB_DATABASE", newName}}
	if dbMode == WorktreeDBFresh {
		return values, "Tiene su propia base de datos, " + newName + ", vacía y migrada.", nil
	}
	phase(fmt.Sprintf("copiando los datos de %s a %s", srcName, newName))
	if err := d.copyDatabase(ctx, ref, srcName, newName, envValueOf(env, "DB_USERNAME"), envValueOf(env, "DB_PASSWORD"), log); err != nil {
		return nil, "", err
	}
	return values, "Tiene su propia base de datos, " + newName + ", copiada de " + srcName + ".", nil
}

// instanceFor is the instance a .env points to: the one on DB_PORT, else the default of its kind.
func (d *Daemon) instanceFor(conn, port string) (InstanceRef, error) {
	kinds := map[string][]versions.Component{"mysql": {versions.MySQL, versions.MariaDB}, "mariadb": {versions.MariaDB, versions.MySQL}, "pgsql": {versions.PostgreSQL}}[conn]
	for _, k := range kinds {
		for _, ref := range d.env.Instances(k) {
			if fmt.Sprint(ref.Instance.Port) == port && ref.Version.Path != "" {
				return ref, nil
			}
		}
	}
	for _, k := range kinds {
		if ref, ok := d.env.DefaultInstance(k); ok {
			return ref, nil
		}
	}
	return InstanceRef{}, fmt.Errorf("el sitio usa %s y Nido no tiene ninguno instalado", kinds[0].Name())
}

// copyDatabase copies a database with its data into another, through a dump in tmp\.
func (d *Daemon) copyDatabase(ctx context.Context, ref InstanceRef, from, to, user, password string, log *os.File) error {
	dump := d.env.Home.Tmp(fmt.Sprintf("copia-%s-%d.sql", from, time.Now().UnixNano()))
	defer os.Remove(dump)
	port := fmt.Sprint(ref.Instance.Port)
	if ref.Kind == versions.PostgreSQL {
		psql := postgres.Psql(ref.Version)
		pgDump := filepath.Join(filepath.Dir(psql), "pg_dump.exe")
		if psql == "" || !fileExists(pgDump) {
			return fmt.Errorf("PostgreSQL %s no trae pg_dump", ref.Version.Version)
		}
		if err := procutil.RunTree(ctx, filepath.Dir(psql), nil, log, pgDump, "-h", "127.0.0.1", "-p", port, "-U", "postgres", "--no-owner", "-f", dump, from); err != nil {
			return fmt.Errorf("pg_dump: %w", err)
		}
		return procutil.RunTree(ctx, filepath.Dir(psql), nil, log, psql, "-h", "127.0.0.1", "-p", port, "-U", "postgres", "-q", "-d", to, "-f", dump)
	}
	client := mysql.ClientExe(ref.Version)
	if client == "" {
		return fmt.Errorf("%s %s no trae el cliente mysql", ref.Kind.Name(), ref.Version.Version)
	}
	var dumper string
	for _, n := range []string{"mariadb-dump.exe", "mysqldump.exe"} {
		if p := filepath.Join(filepath.Dir(client), n); fileExists(p) {
			dumper = p
			break
		}
	}
	if dumper == "" {
		return fmt.Errorf("%s %s no trae mysqldump", ref.Kind.Name(), ref.Version.Version)
	}
	// root has no password in Nido's servers; a project user of its own is used as written in .env.
	auth := []string{"--host=127.0.0.1", "--port=" + port, "--user=root"}
	if user != "" && user != "root" {
		auth = []string{"--host=127.0.0.1", "--port=" + port, "--user=" + user, "--password=" + password}
	}
	args := append(append([]string(nil), auth...), "--single-transaction", "--routines", "--triggers", "--result-file="+dump, from)
	if err := procutil.RunTree(ctx, filepath.Dir(client), nil, log, dumper, args...); err != nil {
		return fmt.Errorf("mysqldump: %w", err)
	}
	load := append([]string{"--host=127.0.0.1", "--port=" + port, "--user=root", to}, "--execute=source "+filepath.ToSlash(dump))
	return procutil.RunTree(ctx, filepath.Dir(client), nil, log, client, load...)
}

// artisanIn runs an Artisan command in a project folder with the PHP of its site.
func (d *Daemon) artisanIn(ctx context.Context, dir, site string, log io.Writer, args ...string) error {
	v, err := d.env.GlobalPHP()
	if s, ferr := d.findSite(site); ferr == nil {
		v, err = d.sitePHP(s)
	}
	if err != nil {
		return err
	}
	ini, err := php.WriteIni(d.env.Home, v, d.env.IniOptions())
	if err != nil {
		return err
	}
	env := []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH()}
	return procutil.RunTree(ctx, dir, env, log, filepath.Join(v.Path, "php.exe"), append([]string{"-c", ini, "artisan"}, args...)...)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// RemoveWorktree stops a worktree's processes, removes it from Git and from Nido. Its database, if it
// had one of its own, is kept (the note says which).
func (d *Daemon) RemoveWorktree(name string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), "."+d.env.Cfg.TLD)
	wt, ok := d.env.Cfg.Worktrees[name]
	if !ok {
		return "", fmt.Errorf("no hay ningún worktree %s.%s (mira 'nido worktree list')", name, d.env.Cfg.TLD)
	}
	if s, err := d.findSite(name); err == nil {
		for _, id := range projectProcesses {
			d.stopProcess(s, id)
		}
	}
	d.StopShare(name)
	var gitErr error
	if parent, err := d.findSite(wt.Site); err == nil && fileExists(wt.Folder) {
		if git := findGit(d.env.Home); git != "" {
			if _, err := procutil.Output(2*time.Minute, parent.Folder, git, "worktree", "remove", "--force", wt.Folder); err != nil {
				gitErr = err
			}
		}
	}
	if fileExists(wt.Folder) && gitErr == nil {
		os.RemoveAll(wt.Folder)
	}
	d.cfgMu.Lock()
	delete(d.env.Cfg.Worktrees, name)
	delete(d.env.Cfg.Sites, name)
	err := d.env.SaveConfig()
	d.cfgMu.Unlock()
	if err != nil {
		return "", err
	}
	if err := d.Rescan(); err != nil {
		d.setProblem("sitios", err)
	}
	d.changed()
	note := ""
	if wt.DB != "" {
		note = "La base de datos " + wt.DB + " se conserva; bórrala en Adminer si ya no la quieres."
	}
	if gitErr != nil {
		return note, fmt.Errorf("Nido ya no sirve %s.%s, pero git worktree remove falló (la carpeta %s sigue ahí): %w", name, d.env.Cfg.TLD, wt.Folder, gitErr)
	}
	return note, nil
}
