package main

import (
	"fmt"

	"github.com/nicotc/laraclon/internal/app"
)

// cmdRecipes lists the project templates of config\recetas.
func cmdRecipes(env *app.Env, args []string) error {
	if len(args) > 0 && args[0] != "list" {
		return fmt.Errorf("uso: nido recipe list")
	}
	var list []app.Recipe
	if c, err := app.Connect(env.Home.Root); err == nil {
		if err := c.Call("GET", "/api/recipes", nil, &list); err != nil {
			return err
		}
	} else {
		d, err := app.NewDaemon(env)
		if err != nil {
			return err
		}
		list = d.Recipes()
	}
	for _, r := range list {
		if r.Error != "" {
			fmt.Printf("  %-24s ! %s\n", r.ID, r.Error)
			continue
		}
		fmt.Printf("  %-24s %s (%s)\n", r.ID, r.Name, r.Source.Type)
	}
	fmt.Printf("\nCrea un proyecto con 'nido new <nombre> --receta <archivo>'. Las recetas están en %s\n", env.Home.Config("recetas"))
	return nil
}

// cmdSiteTemplate saves a site as a template for new projects.
func cmdSiteTemplate(env *app.Env, site, name string) error {
	c, err := app.Connect(env.Home.Root)
	if err != nil {
		return fmt.Errorf("Nido no está corriendo: inícialo con 'nido run'")
	}
	var r app.Recipe
	if err := c.Call("POST", "/api/sites/template", map[string]string{"site": site, "name": name}, &r); err != nil {
		return err
	}
	fmt.Printf("Plantilla «%s» guardada en %s\nCrea proyectos con ella: nido new <nombre> --receta %s\n", r.Name, env.Home.Config("plantillas", r.ID), r.ID)
	return nil
}
