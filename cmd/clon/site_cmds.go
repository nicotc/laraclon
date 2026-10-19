package main

import (
	"fmt"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

const siteUsage = `
  clon site server <sitio> apache|nginx   servidor web de un sitio (la dirección .test no cambia)
`

func cmdSite(env *app.Env, args []string) error {
	if len(args) != 3 || args[0] != "server" {
		return fmt.Errorf("uso:%s", siteUsage)
	}
	site, server := args[1], strings.ToLower(args[2])
	if c, err := app.Connect(env.Home.Root); err == nil {
		if err := c.Call("POST", "/api/sites/server", map[string]string{"site": site, "server": server}, nil); err != nil {
			return err
		}
		fmt.Printf("%s usa ahora %s\n", site, server)
		return nil
	}
	// clon is not running: only the setting changes; it applies on the next start.
	if server != config.ServerApache && server != config.ServerNginx {
		return fmt.Errorf("servidor desconocido %q: usa apache o nginx", server)
	}
	name := strings.TrimSuffix(strings.ToLower(site), "."+env.Cfg.TLD)
	list, _ := sites.Scan(env.Cfg)
	found := false
	for _, s := range list {
		found = found || s.Name == name
	}
	if !found {
		return fmt.Errorf("no hay ningún sitio %s.%s", name, env.Cfg.TLD)
	}
	if _, ok := env.Reg.Find(versions.Nginx, env.Cfg.Nginx.Version); server == config.ServerNginx && !ok {
		return fmt.Errorf("Nginx no está instalado: 'clon service install nginx 1.30'")
	}
	env.Cfg.SetSiteServer(name, server)
	if err := env.SaveConfig(); err != nil {
		return err
	}
	fmt.Printf("%s.%s usará %s cuando inicies clon\n", name, env.Cfg.TLD, server)
	return nil
}
