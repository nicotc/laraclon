package main

import (
	"fmt"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
)

const newUsage = `
  clon new <nombre> [--kit react|vue|svelte|livewire|ninguno] [--db sqlite|mysql|mariadb|postgresql] [--carpeta <raíz>]
                                          proyecto Laravel nuevo en https://<nombre>.test, con su base de datos y .env
`

// cmdNew creates a Laravel project through the running clon, which owns the databases it needs.
func cmdNew(env *app.Env, args []string) error {
	var p app.NewProject
	for i := 0; i < len(args); i++ {
		flag, value := args[i], ""
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
