package main

import (
	"fmt"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
)

const newUsage = `
  clon new <nombre> [--kit react|vue|svelte|livewire|ninguno] [--db sqlite|mysql|mariadb|postgresql] [--carpeta <raíz>]
           [--pnpm | --yarn]              proyecto Laravel nuevo en https://<nombre>.test, con su base de datos y .env
                                          (el frontend se instala con npm, o con pnpm o yarn, como 'laravel new')
  clon new <nombre> --wordpress [--idioma es_ES] [--db mysql|mariadb]
                                          WordPress con WP-CLI: base de datos, wp-config.php y un usuario admin
  clon new <nombre> --symfony [--webapp] [--db sqlite|mysql|mariadb|postgresql]
                                          Symfony (symfony/skeleton; --webapp añade Twig, Doctrine, formularios…)
  clon new <nombre> --git <url> [--rama <rama>] [--db …]
                                          clona un repositorio y lo prepara (Composer, .env, base de datos, Vite)
  clon new <nombre> --receta <archivo> [--db …]
                                          desde una plantilla propia de config\recetas ('clon recipe list')
`

// cmdNew creates a project through the running clon, which owns the databases it needs.
func cmdNew(env *app.Env, args []string) error {
	var p app.NewProject
	for i := 0; i < len(args); i++ {
		flag, value := args[i], ""
		switch flag {
		case "--npm", "--pnpm", "--yarn": // the same options as 'laravel new'
			p.PM = strings.TrimPrefix(flag, "--")
			continue
		case "--wordpress", "--wp":
			p.Type = "wordpress"
			continue
		case "--symfony":
			p.Type = "symfony"
			continue
		case "--webapp":
			p.Webapp = true
			continue
		}
		if k, v, ok := strings.Cut(flag, "="); ok && strings.HasPrefix(flag, "--") {
			flag, value = k, v
		} else if strings.HasPrefix(flag, "--") && i+1 < len(args) {
			value = args[i+1]
			i++
		}
		switch flag {
		case "--kit":
			p.Kit = value
		case "--db":
			p.DB = value
		case "--carpeta", "--root":
			p.Root = value
		case "--git":
			p.Git = value
		case "--rama", "--branch":
			p.Branch = value
		case "--idioma", "--locale":
			p.Locale = value
		case "--receta", "--recipe":
			p.Recipe = value
		default:
			if strings.HasPrefix(flag, "--") || p.Name != "" {
				return fmt.Errorf("uso:%s", newUsage)
			}
			p.Name = flag
		}
	}
	if p.Name == "" {
		return fmt.Errorf("uso:%s", newUsage)
	}
	c, err := app.Connect(env.Home.Root)
	if err != nil {
		return fmt.Errorf("clon no está corriendo: inícialo con 'clon run' (crea la base de datos del proyecto)")
	}
	var j app.Job
	if err := c.Call("POST", "/api/projects", p, &j); err != nil {
		return err
	}
	return followJob(remoteOps{c}, j)
}
