package main

import (
	"context"
	"fmt"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/php"
)

type extList struct {
	Version    string          `json:"version"`
	Extensions []php.Extension `json:"extensions"`
}

// cmdPHPExt lists or switches the extensions of a PHP version (the global one by default):
//
//	clon php ext [versión]
//	clon php ext on|off <nombre> [versión]
func cmdPHPExt(ctx context.Context, env *app.Env, args []string) error {
	switchTo := len(args) >= 2 && (args[0] == "on" || args[0] == "off")
	version := ""
	switch {
	case switchTo && len(args) > 2:
		version = args[2]
	case !switchTo && len(args) > 0:
		version = args[0]
	}
	var out extList
	if c, err := app.Connect(env.Home.Root); err == nil {
		// With clon running, the php-cgi processes of that version restart with the change.
		if switchTo {
			body := map[string]any{"version": version, "name": args[1], "enabled": args[0] == "on"}
			if err := c.Call("POST", "/api/php/extensions", body, &out); err != nil {
				return err
			}
		} else if err := c.Call("GET", "/api/php/extensions?version="+version, nil, &out); err != nil {
			return err
		}
	} else {
		v, err := env.PHPVersion(version)
		if err != nil {
			return err
		}
		if switchTo {
			if err := env.SetPHPExtension(ctx, v, args[1], args[0] == "on"); err != nil {
				return err
			}
			if _, err := env.RefreshShims(); err != nil {
				return err
			}
		}
		out = extList{Version: v.Version, Extensions: env.PHPExtensions(v)}
	}
	fmt.Printf("Extensiones de PHP %s ([x] = activa):\n", out.Version)
	for _, x := range out.Extensions {
		mark := "[ ]"
		if x.Enabled {
			mark = "[x]"
		}
		note := x.Source
		if x.Default {
			note += ", activa por defecto"
		}
		fmt.Printf("  %s %-14s %s\n", mark, x.Name, note)
	}
	fmt.Println("\nCámbialas con 'clon php ext on|off <nombre> [versión]'; las «descargables» se descargan al activarlas.")
	return nil
}
