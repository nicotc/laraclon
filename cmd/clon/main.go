// clon is the command line of the local development environment.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/download"
	"github.com/nicotc/laraclon/internal/hosts"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/laragon"
	"github.com/nicotc/laraclon/internal/vcredist"
	"github.com/nicotc/laraclon/internal/versions"
)

const usage = `clon: entorno de desarrollo local para Windows

Uso:
  clon setup                        primera configuración (Visual C++, Laragon o PHP y Apache, Node LTS, Composer)
  clon vcredist status | install    runtime de Visual C++ que necesitan PHP, Apache y MySQL
  clon import laragon [ruta]        usa las versiones y carpetas de proyectos de Laragon
  clon versions                     lista todo lo instalado

  clon php list | remote            versiones de PHP instaladas | publicadas en windows.php.net
  clon php install <rama>           descarga PHP (por ejemplo 8.4)
  clon php use <versión>            cambia el PHP global
  clon php ext [versión]            extensiones de PHP; 'clon php ext on|off xdebug [versión]' las cambia

  clon node list | remote           versiones de Node instaladas | publicadas en nodejs.org (con avisos)
  clon node install [lts|latest|24] descarga Node con npm y npx ('--use' lo hace global)
  clon node use <versión>           cambia el Node global
  clon node uninstall <versión>     borra una versión descargada
  clon node install pnpm|yarn       instala o actualiza pnpm o yarn ('clon node uninstall pnpm' lo quita)

  clon composer install             descarga o actualiza Composer
  clon shims                        regenera los atajos de shims\ (php, composer, node, npm, npx…)
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage + newUsage + apacheUsage + daemonUsage + siteUsage + serviceUsage + mysqlUsage)
		return nil
	}
	// The elevated helper only rewrites clon's block in the hosts file; it touches nothing else.
	if args[0] == "hosts-apply" {
		if len(args) != 2 {
			return fmt.Errorf("uso interno: clon hosts-apply <archivo>")
		}
		return hosts.Apply(args[1])
	}
	env, err := app.Open()
	if err != nil {
		return err
	}
	switch args[0] {
	case "run":
		return cmdRun(ctx, env, args[1:])
	case "start", "stop", "restart", "rescan":
		return cmdService(env, args[0])
	case "quit":
		return cmdQuit(env)
	case "status":
		return cmdStatus(env)
	case "sites":
		return cmdSites(env)
	case "ca":
		return cmdCA(env, args[1:])
	case "hosts":
		return cmdHosts(env, args[1:])
	case "path":
		return cmdPath(env, args[1:])
	case "mysql":
		return cmdMySQL(env, args[1:])
	case "service", "services":
		return cmdServices(env, args[1:])
	case "site":
		return cmdSite(env, args[1:])
	case "new":
		return cmdNew(env, args[1:])
	case "recipe", "recipes", "receta", "recetas":
		return cmdRecipes(env, args[1:])
	case "version":
		fmt.Println("clon 0.1.0 (fase 1) · instalación:", env.Home.Root)
		return nil
	case "setup":
		return cmdSetup(ctx, env)
	case "import":
		return cmdImport(env, args[1:])
	case "versions":
		return cmdVersions(env)
	case "php":
		return cmdPHP(ctx, env, args[1:])
	case "apache":
		return cmdApache(env, args[1:])
	case "node":
		return cmdNode(ctx, env, args[1:])
	case "composer":
		return cmdComposer(ctx, env, args[1:])
	case "shims":
		return refreshShims(env)
	case "vcredist":
		return cmdVCRedist(ctx, env, args[1:])
	}
	return fmt.Errorf("comando desconocido %q (usa 'clon help')", args[0])
}

func cmdSetup(ctx context.Context, env *app.Env) error {
	// PHP, Apache and MySQL cannot even start without the Visual C++ runtime, so it goes first.
	if missing := vcredist.Missing(); len(missing) > 0 {
		fmt.Printf("Falta el runtime de Visual C++ x64 (%s), necesario para PHP, Apache y MySQL.\n", strings.Join(missing, ", "))
		fmt.Println("Instalando la versión oficial de Microsoft (Windows pedirá permiso de administrador)…")
		if err := vcredist.Install(ctx, env.Home, "", printProgress(), vcStatus(true)); err != nil {
			fmt.Println()
			return err
		}
		fmt.Println("\nRuntime de Visual C++ instalado:", vcredist.Version())
	}
	if _, err := os.Stat(laragon.DefaultRoot); err == nil && len(env.Reg.List(versions.PHP)) == 0 {
		fmt.Println("Laragon encontrado en", laragon.DefaultRoot)
		if err := cmdImport(env, []string{"laragon"}); err != nil {
			return err
		}
	}
	// Without Laragon, clon brings its own PHP (windows.php.net) and Apache (Apache Lounge).
	if len(env.Reg.List(versions.PHP)) == 0 {
		if err := cmdPHP(ctx, env, []string{"install", "latest"}); err != nil {
			return err
		}
	}
	if len(env.Reg.List(versions.Apache)) == 0 {
		if err := cmdApache(env, []string{"install"}); err != nil {
			return err
		}
	}
	if len(env.Reg.List(versions.Node)) == 0 || !installedFromDownload(env, versions.Node) {
		if err := cmdNode(ctx, env, []string{"install", "lts"}); err != nil {
			return err
		}
	}
	if err := cmdComposer(ctx, env, []string{"install"}); err != nil {
		return err
	}
	if !install.HaveCABundle(env.Home) {
		fmt.Println("Descargando la lista de autoridades públicas (curl.se) para PHP y Composer…")
		if err := install.InstallCABundle(ctx, env.Home); err != nil {
			return err
		}
	}
	// The local CA may already exist (the Node step creates it): PHP's bundle gets the public list.
	if err := env.RefreshCABundleFile(); err != nil {
		return err
	}
	if err := refreshShims(env); err != nil {
		return err
	}
	fmt.Println("\nListo. Para usar php, composer, node, npm y npx en cualquier terminal, añade a tu PATH:")
	fmt.Println("  ", env.Home.Shims())
	fmt.Println("  ", env.NPMPrefix(), " (paquetes globales de npm)")
	return nil
}

func installedFromDownload(env *app.Env, c versions.Component) bool {
	for _, v := range env.Reg.List(c) {
		if v.Source == "download" {
			return true
		}
	}
	return false
}

func cmdImport(env *app.Env, args []string) error {
	if len(args) == 0 || args[0] != "laragon" {
		return fmt.Errorf("uso: clon import laragon [ruta]")
	}
	root := laragon.DefaultRoot
	if len(args) > 1 {
		root = args[1]
	}
	rep, err := laragon.Import(root, env.Reg, &env.Cfg)
	if err != nil {
		return err
	}
	env.SyncServices()
	if err := env.SaveRegistry(); err != nil {
		return err
	}
	if err := env.SaveConfig(); err != nil {
		return err
	}
	fmt.Printf("Importado desde %s (los binarios se usan en su sitio; no se copia ni se modifica nada):\n", root)
	for _, v := range rep.Versions {
		fmt.Printf("  %-34s %s\n", v.Label(), v.Path)
	}
	for _, r := range rep.Roots {
		fmt.Println("  carpeta de proyectos:", r)
	}
	for c, v := range rep.Defaults {
		fmt.Printf("  versión predeterminada de %s: %s\n", c, v)
	}
	for _, s := range rep.Skipped {
		fmt.Println("  omitido:", s)
	}
	if rep.MissingRuntime {
		fmt.Println("\nPHP, Apache y MySQL necesitan el runtime de Visual C++ x64, que falta en este equipo.")
		fmt.Println("Instálalo con 'clon vcredist install' y vuelve a ejecutar 'clon import laragon'.")
	}
	return nil
}

func cmdVersions(env *app.Env) error {
	comps := []versions.Component{versions.PHP, versions.Apache, versions.MySQL, versions.MariaDB, versions.Nginx, versions.Redis, versions.Node}
	for _, c := range comps {
		list := env.Reg.List(c)
		if len(list) == 0 {
			continue
		}
		active := activeVersion(env, c)
		for _, v := range list {
			mark := " "
			if v.Version == active {
				mark = "*"
			}
			fmt.Printf("%s %-34s %-9s %s\n", mark, v.Label(), v.Source, v.Path)
		}
	}
	fmt.Println("\n* = versión activa")
	return nil
}

func activeVersion(env *app.Env, c versions.Component) string {
	var v versions.Version
	var err error
	switch c {
	case versions.PHP:
		v, err = env.GlobalPHP()
	case versions.Node:
		v, err = env.GlobalNode()
	case versions.MySQL, versions.MariaDB:
		def := env.Cfg.Service(string(c)).Default
		if def == "" {
			return ""
		}
		v, err = env.InstanceVersion(c, def)
	case versions.Apache:
		var ok bool
		v, ok = env.Reg.Find(versions.Apache, env.Cfg.Apache.Version)
		if !ok {
			return ""
		}
	default:
		return ""
	}
	if err != nil {
		return ""
	}
	return v.Version
}

func cmdPHP(ctx context.Context, env *app.Env, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "ext", "extensions":
		return cmdPHPExt(ctx, env, args[1:])
	case "list":
		active := activeVersion(env, versions.PHP)
		for _, v := range env.Reg.List(versions.PHP) {
			mark := " "
			if v.Version == active {
				mark = "*"
			}
			fmt.Printf("%s %-26s %s\n", mark, v.Label(), v.Path)
		}
		return nil
	case "remote":
		branches, err := install.PHPBranches(ctx)
		if err != nil {
			return err
		}
		for _, b := range branches {
			fmt.Printf("  %-5s %s\n", b.Branch, b.Version)
		}
		return nil
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("uso: clon php install <rama>, por ejemplo 8.4")
		}
		if args[1] == "latest" {
			fmt.Println("Descargando la versión más reciente de PHP (build NTS x64 de windows.php.net)…")
		} else {
			fmt.Printf("Descargando PHP %s (build NTS x64 de windows.php.net)…\n", args[1])
		}
		v, err := install.InstallPHP(ctx, env.Home, args[1], printProgress())
		fmt.Println()
		if err != nil {
			return err
		}
		env.Reg.Add(v)
		if env.Cfg.PHP.Version == "" {
			env.Cfg.PHP.Version = v.Version
		}
		if err := saveAll(env); err != nil {
			return err
		}
		fmt.Println("Instalado:", v.Label(), "en", v.Path)
		return refreshShims(env)
	case "use":
		if len(args) < 2 {
			return fmt.Errorf("uso: clon php use <versión>, por ejemplo 8.3")
		}
		v, ok := env.Reg.Find(versions.PHP, args[1])
		if !ok {
			return fmt.Errorf("PHP %s no está instalado (mira 'clon php list')", args[1])
		}
		// With clon running, the switch also restarts the web pool.
		if c, err := app.Connect(env.Home.Root); err == nil {
			var out map[string]string
			if err := c.Call("POST", "/api/php", map[string]string{"version": v.Version}, &out); err != nil {
				return err
			}
			fmt.Println("PHP global:", v.Label(), "(web, terminal y Composer)")
			return nil
		}
		env.Cfg.PHP.Version = v.Version
		if err := env.SaveConfig(); err != nil {
			return err
		}
		fmt.Println("PHP global:", v.Label())
		return refreshShims(env)
	}
	return fmt.Errorf("subcomando de php desconocido: %s", args[0])
}

func cmdComposer(ctx context.Context, env *app.Env, args []string) error {
	if len(args) == 0 || (args[0] != "install" && args[0] != "update") {
		return fmt.Errorf("uso: clon composer install")
	}
	fmt.Println("Descargando Composer (última versión estable de getcomposer.org)…")
	path, err := install.InstallComposer(ctx, env.Home)
	if err != nil {
		return err
	}
	fmt.Println("Composer en", path)
	// Composer 2.2 LTS, for projects pinned to a PHP older than 7.2.5, stays up to date too.
	if _, err := os.Stat(install.ComposerLTSPhar(env.Home)); err == nil {
		if path, err := install.InstallComposerLTS(ctx, env.Home); err == nil {
			fmt.Println("Composer 2.2 LTS en", path)
		} else {
			fmt.Println("No se pudo actualizar Composer 2.2 LTS:", err)
		}
	}
	return refreshShims(env)
}

func refreshShims(env *app.Env) error {
	targets, err := env.RefreshShims()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(targets))
	for n := range targets {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println("Atajos en", env.Home.Shims()+":", strings.Join(names, ", "))
	return nil
}

func saveAll(env *app.Env) error {
	if err := env.SaveRegistry(); err != nil {
		return err
	}
	return env.SaveConfig()
}

// printProgress shows download progress on a single console line.
func printProgress() download.Progress {
	last := time.Time{}
	return func(done, total int64) {
		if time.Since(last) < 200*time.Millisecond && done != total {
			return
		}
		last = time.Now()
		if total > 0 {
			fmt.Printf("\r  %5.1f MB de %5.1f MB (%3d%%)", float64(done)/1e6, float64(total)/1e6, done*100/total)
		} else {
			fmt.Printf("\r  %5.1f MB", float64(done)/1e6)
		}
	}
}
