package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

const siteUsage = `
  nido site server <sitio> apache|nginx|frankenphp   servidor web de un sitio (la dirección .test no cambia)
  nido site php <sitio> <versión>|global             PHP propio para un proyecto antiguo, o vuelta al global
  nido site template <sitio> [nombre]     guarda el proyecto como plantilla (sin vendor, node_modules ni .env)
  nido site proc <sitio> vite|queue|schedule|reverb on|off
                                          npm run dev (con HTTPS), queue:work, schedule:work o Reverb (wss://reverb.<sitio>.test)
  nido site debug <sitio> on|off                     Xdebug solo para ese sitio (el editor escucha en el 9003)
  nido site setup <sitio> [--db mysql|…]             prepara un proyecto existente: Composer, .env, base de datos, Vite
`

func cmdSite(env *app.Env, args []string) error {
	if len(args) == 4 && args[0] == "proc" {
		return cmdSiteProc(env, args[1], strings.ToLower(args[2]), args[3])
	}
	if (len(args) == 2 || len(args) == 3) && args[0] == "template" {
		name := ""
		if len(args) == 3 {
			name = args[2]
		}
		return cmdSiteTemplate(env, args[1], name)
	}
	if len(args) >= 2 && args[0] == "setup" {
		db := ""
		if len(args) == 4 && args[2] == "--db" {
			db = args[3]
		} else if len(args) != 2 {
			return fmt.Errorf("uso:%s", siteUsage)
		}
		c, err := app.Connect(env.Home.Root)
		if err != nil {
			return fmt.Errorf("Nido no está corriendo: inícialo con 'nido run' (prepara la base de datos del proyecto)")
		}
		var j app.Job
		if err := c.Call("POST", "/api/projects/setup", map[string]string{"site": args[1], "db": db}, &j); err != nil {
			return err
		}
		return followJob(remoteOps{c}, j)
	}
	if len(args) == 3 && args[0] == "debug" {
		on := args[2] == "on" || args[2] == "si" || args[2] == "sí"
		c, err := app.Connect(env.Home.Root)
		if err != nil {
			return fmt.Errorf("Nido no está corriendo: inícialo con 'nido run'")
		}
		if err := c.Call("POST", "/api/sites/debug", map[string]any{"site": args[1], "on": on}, nil); err != nil {
			return err
		}
		if on {
			fmt.Printf("%s corre con Xdebug: cada petición se conecta al editor en 127.0.0.1:9003. Para la terminal, usa phpx.\n", args[1])
		} else {
			fmt.Printf("%s vuelve a correr sin Xdebug\n", args[1])
		}
		return nil
	}
	if len(args) != 3 || (args[0] != "server" && args[0] != "php") {
		return fmt.Errorf("uso:%s", siteUsage)
	}
	if args[0] == "php" {
		return cmdSitePHP(env, args[1], args[2])
	}
	site, server := args[1], strings.ToLower(args[2])
	if c, err := app.Connect(env.Home.Root); err == nil {
		if err := c.Call("POST", "/api/sites/server", map[string]string{"site": site, "server": server}, nil); err != nil {
			return err
		}
		fmt.Printf("%s usa ahora %s\n", site, server)
		return nil
	}
	// Nido is not running: only the setting changes; it applies on the next start.
	needs := map[string]versions.Component{config.ServerApache: "", config.ServerNginx: versions.Nginx, config.ServerFrankenPHP: versions.FrankenPHP}
	kind, known := needs[server]
	if !known {
		return fmt.Errorf("servidor desconocido %q: usa apache, nginx o frankenphp", server)
	}
	s, err := findSite(env, site)
	if err != nil {
		return err
	}
	if _, ok := env.Reg.Find(kind, ""); kind != "" && !ok {
		return fmt.Errorf("%s no está instalado: 'nido service install %s <versión>'", kind.Name(), kind)
	}
	env.Cfg.SetSiteServer(s.Name, server)
	if err := env.SaveConfig(); err != nil {
		return err
	}
	fmt.Printf("%s usará %s cuando inicies Nido\n", s.Host, server)
	return nil
}

// cmdSitePHP pins a site's PHP version ("global" goes back to the global PHP).
func cmdSitePHP(env *app.Env, site, want string) error {
	if strings.EqualFold(want, "global") {
		want = ""
	}
	if c, err := app.Connect(env.Home.Root); err == nil {
		if err := c.Call("POST", "/api/sites/php", map[string]string{"site": site, "php": want}, nil); err != nil {
			return err
		}
	} else {
		s, err := findSite(env, site)
		if err != nil {
			return err
		}
		if want != "" {
			if s.Server == config.ServerFrankenPHP {
				return fmt.Errorf("%s usa FrankenPHP, que trae su propio PHP; pásalo a Apache o Nginx para fijarle otro", s.Host)
			}
			v, ok := env.Reg.Find(versions.PHP, want)
			if !ok {
				return fmt.Errorf("PHP %s no está instalado (instálalo con 'nido php install %s')", want, versions.MajorMinor(want))
			}
			if err := env.EnsureComposerFor(context.Background(), v.Version); err != nil {
				fmt.Println("No se pudo descargar Composer 2.2 LTS (para PHP anterior a 7.2.5):", err)
			}
		}
		env.Cfg.SetSitePHP(s.Name, want)
		if err := env.SaveConfig(); err != nil {
			return err
		}
		if err := refreshShims(env); err != nil {
			return err
		}
	}
	if want == "" {
		fmt.Printf("%s vuelve al PHP global\n", site)
		return nil
	}
	fmt.Printf("%s usa PHP %s: en la web y, dentro de su carpeta, con php, composer y artisan\n", site, want)
	return nil
}

// cmdSiteProc turns a project process on (it runs now and starts with Nido) or off.
func cmdSiteProc(env *app.Env, site, id, state string) error {
	on := state == "on" || state == "si" || state == "sí"
	if !on && state != "off" && state != "no" {
		return fmt.Errorf("uso:%s", siteUsage)
	}
	if c, err := app.Connect(env.Home.Root); err == nil {
		if err := c.Call("POST", "/api/sites/process", map[string]any{"site": site, "id": id, "on": on}, nil); err != nil {
			return err
		}
		if on {
			name := strings.TrimSuffix(strings.ToLower(site), "."+env.Cfg.TLD)
			fmt.Printf("%s: %s en marcha; arranca también con «Iniciar todo» (registro en %s)\n", site, id, env.Home.Logs(name+"-"+id+".log"))
			if id == "reverb" {
				fmt.Printf("Reverb responde en wss://reverb.%s.%s; el .env del proyecto ya apunta ahí\n", name, env.Cfg.TLD)
				// The compiled frontend is rebuilt with the new address when it had another one.
				var jobs []app.Job
				c.Call("GET", "/api/jobs", nil, &jobs)
				for _, j := range jobs {
					if j.Kind == "project" && j.Series == name && !j.Finished {
						return followJob(remoteOps{c}, j)
					}
				}
			}
		} else {
			fmt.Printf("%s: %s detenido\n", site, id)
		}
		return nil
	}
	s, err := findSite(env, site)
	if err != nil {
		return err
	}
	env.Cfg.SetSiteProcess(s.Name, id, on)
	if err := env.SaveConfig(); err != nil {
		return err
	}
	fmt.Printf("%s: %s %s cuando inicies Nido\n", s.Host, id, map[bool]string{true: "arrancará", false: "no arrancará"}[on])
	return nil
}

// findSite resolves "tienda" or "tienda.test" among the sites of the project roots.
func findSite(env *app.Env, site string) (sites.Site, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(site)), "."+env.Cfg.TLD)
	list, _ := sites.Scan(env.Cfg)
	for _, s := range list {
		if s.Name == name {
			return s, nil
		}
	}
	return sites.Site{}, fmt.Errorf("no hay ningún sitio %s.%s", name, env.Cfg.TLD)
}
