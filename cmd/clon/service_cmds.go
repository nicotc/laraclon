package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/versions"
)

const serviceUsage = `
  clon service list                       bases de datos: versiones, puertos y estado
  clon service remote [mysql|mariadb]     versiones que se pueden instalar (descargas oficiales)
  clon service install mysql 8.4          instala una serie o versión de MySQL, MariaDB, Nginx o FrankenPHP
  clon service start | stop <servicio>    inicia o detiene una instancia, por ejemplo mysql-8.4
  clon service port <servicio> <puerto>   cambia su puerto
  clon service default <servicio>         la hace predeterminada (puerto 3306 y atajo mysql)
  clon service autostart <servicio> on|off  que arranque o no con «Iniciar todo»
  clon service uninstall <servicio>       quita esa serie (sus datos se conservan)
`

// serviceOps runs the database actions through the running clon (API) or, when it is not running,
// on a local daemon object that only edits settings and files.
type serviceOps interface {
	status() (app.Status, error)
	catalog(kind versions.Component, refresh bool) ([]install.Release, error)
	install(kind versions.Component, version string) (app.Job, error)
	jobs() ([]app.Job, error)
	action(name, key string) error
	settings(key string, port *int, autostart *bool) error
}

type remoteOps struct{ c *app.Client }

func (r remoteOps) status() (app.Status, error) {
	var st app.Status
	return st, r.c.Call("GET", "/api/status", nil, &st)
}

func (r remoteOps) catalog(kind versions.Component, refresh bool) ([]install.Release, error) {
	var list []install.Release
	q := "/api/catalog?kind=" + string(kind)
	if refresh {
		q += "&refresh=1"
	}
	return list, r.c.Call("GET", q, nil, &list)
}

func (r remoteOps) install(kind versions.Component, version string) (app.Job, error) {
	var j app.Job
	return j, r.c.Call("POST", "/api/services/install", map[string]string{"kind": string(kind), "version": version}, &j)
}

func (r remoteOps) jobs() ([]app.Job, error) {
	var list []app.Job
	return list, r.c.Call("GET", "/api/jobs", nil, &list)
}

func (r remoteOps) action(name, key string) error {
	return r.c.Call("POST", "/api/services/"+name, map[string]string{"key": key}, nil)
}

func (r remoteOps) settings(key string, port *int, autostart *bool) error {
	body := map[string]any{"key": key}
	if port != nil {
		body["port"] = *port
	}
	if autostart != nil {
		body["autostart"] = *autostart
	}
	return r.c.Call("POST", "/api/services/settings", body, nil)
}

type localOps struct{ d *app.Daemon }

func (l localOps) status() (app.Status, error) { return l.d.Status(), nil }
func (l localOps) catalog(kind versions.Component, refresh bool) ([]install.Release, error) {
	return l.d.Catalog(context.Background(), kind, refresh)
}
func (l localOps) install(kind versions.Component, version string) (app.Job, error) {
	return l.d.InstallService(kind, version)
}
func (l localOps) jobs() ([]app.Job, error) { return l.d.Jobs(), nil }
func (l localOps) action(name, key string) error {
	switch name {
	case "start":
		return fmt.Errorf("clon no está corriendo: inícialo con 'clon run' (arranca las bases de datos predeterminadas)")
	case "stop":
		return nil
	case "default":
		return l.d.SetDefaultService(key)
	case "uninstall":
		return l.d.UninstallService(key)
	}
	return fmt.Errorf("acción desconocida: %s", name)
}
func (l localOps) settings(key string, port *int, autostart *bool) error {
	return l.d.ServiceSettings(key, port, autostart)
}

func serviceBackend(env *app.Env) (serviceOps, error) {
	if c, err := app.Connect(env.Home.Root); err == nil {
		return remoteOps{c}, nil
	}
	d, err := app.NewDaemon(env)
	if err != nil {
		return nil, err
	}
	return localOps{d}, nil
}

func cmdServices(env *app.Env, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	ops, err := serviceBackend(env)
	if err != nil {
		return err
	}
	need := func(n int, usage string) error {
		if len(args) < n {
			return fmt.Errorf("uso: clon service %s", usage)
		}
		return nil
	}
	switch args[0] {
	case "list":
		st, err := ops.status()
		if err != nil {
			return err
		}
		printDatabases(st)
		return nil
	case "remote":
		kinds := install.Installable
		if len(args) > 1 {
			kinds = []versions.Component{versions.Component(strings.ToLower(args[1]))}
		}
		for _, k := range kinds {
			list, err := ops.catalog(k, len(args) > 2 && args[2] == "--refresh")
			if err != nil {
				return err
			}
			fmt.Printf("%s (%s):\n", k.Name(), sourceOf(list))
			for _, r := range list {
				size := ""
				if r.Size > 0 {
					size = fmt.Sprintf("%.0f MB", float64(r.Size)/1e6)
				}
				fmt.Printf("  %-6s %-9s %-22s %s\n", r.Series, r.Version, r.Note, size)
			}
		}
		return nil
	case "install":
		if err := need(3, "install mysql|mariadb <serie o versión>, por ejemplo: clon service install mysql 8.4"); err != nil {
			return err
		}
		j, err := ops.install(versions.Component(strings.ToLower(args[1])), args[2])
		if err != nil {
			return err
		}
		return followJob(ops, j)
	case "start", "stop", "default", "uninstall":
		if err := need(2, args[0]+" <servicio>, por ejemplo mysql-8.4"); err != nil {
			return err
		}
		if err := ops.action(args[0], args[1]); err != nil {
			return err
		}
		st, err := ops.status()
		if err != nil {
			return err
		}
		printDatabases(st)
		return nil
	case "port":
		if err := need(3, "port <servicio> <puerto>"); err != nil {
			return err
		}
		port, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("puerto no válido: %s", args[2])
		}
		return ops.settings(args[1], &port, nil)
	case "autostart":
		if err := need(3, "autostart <servicio> on|off"); err != nil {
			return err
		}
		on := args[2] == "on" || args[2] == "si" || args[2] == "sí"
		return ops.settings(args[1], nil, &on)
	}
	return fmt.Errorf("uso:%s", serviceUsage)
}

func sourceOf(list []install.Release) string {
	if len(list) > 0 {
		return "descargas de " + list[0].Source
	}
	return "sin versiones"
}

// followJob prints an installation's progress until it ends.
func followJob(ops serviceOps, j app.Job) error {
	fmt.Printf("Instalando %s %s…\n", versions.Component(j.Kind).Name(), j.Version)
	phase := ""
	for {
		list, err := ops.jobs()
		if err != nil {
			return err
		}
		for _, cur := range list {
			if cur.ID != j.ID {
				continue
			}
			if cur.Phase != phase && cur.Phase != "descargando" && !cur.Finished {
				if phase == "descargando" {
					fmt.Println()
				}
				fmt.Println(" ", cur.Phase+"…")
			}
			if cur.Phase == "descargando" && cur.Total > 0 {
				fmt.Printf("\r  %5.1f MB de %5.1f MB (%3d%%)", float64(cur.Done)/1e6, float64(cur.Total)/1e6, cur.Done*100/cur.Total)
			}
			phase = cur.Phase
			if cur.Finished {
				if cur.Error != "" {
					return fmt.Errorf("%s", cur.Error)
				}
				kind := versions.Component(cur.Kind)
				if kind == versions.Nginx || kind == versions.FrankenPHP {
					fmt.Printf("Instalado: %s %s. Elígelo en un sitio con 'clon site server <sitio> %s'\n", kind.Name(), cur.Version, kind)
					return nil
				}
				fmt.Printf("Instalado: %s %s (servicio %s)\n", kind.Name(), cur.Version, app.InstanceKey(kind, cur.Series))
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func printDatabases(st app.Status) {
	empty := true
	for _, g := range st.Databases {
		if len(g.Instances) == 0 {
			continue
		}
		empty = false
		fmt.Printf("%s:\n", g.Name)
		for _, in := range g.Instances {
			mark := " "
			if in.Default {
				mark = "*"
			}
			start := "a mano"
			if in.Autostart {
				start = "con «Iniciar todo»"
			}
			fmt.Printf("%s %-14s %-9s puerto %-5d %-11s %s\n", mark, in.Key, in.Version, in.Port, in.State, start)
			if in.External {
				fmt.Println("    datos:", in.DataDir)
			}
			if in.Error != "" {
				fmt.Println("    !", firstLine(in.Error))
			}
		}
	}
	if empty {
		fmt.Println("No hay bases de datos instaladas. Mira 'clon service remote' e instala una con 'clon service install mysql 8.4'.")
		return
	}
	fmt.Println("\n* = predeterminada (atajo mysql y puerto estándar)")
}
