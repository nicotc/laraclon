package main

import (
	"fmt"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/versions"
)

const apacheUsage = `
  nido apache list | remote               versiones de Apache instaladas | la que publica Apache Lounge
  nido apache install [versión]           descarga Apache de Apache Lounge (la más reciente si no se indica)
  nido apache use <versión>               cambia el Apache que atiende los sitios
`

// cmdApache lists, installs and switches the Apache that serves the sites.
func cmdApache(env *app.Env, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		list := env.Reg.List(versions.Apache)
		if len(list) == 0 {
			fmt.Println("No hay ningún Apache instalado: descarga el de Apache Lounge con 'nido apache install'.")
			return nil
		}
		active := activeVersion(env, versions.Apache)
		for _, v := range list {
			mark := " "
			if v.Version == active {
				mark = "*"
			}
			fmt.Printf("%s %-26s %-9s %s\n", mark, v.Label(), v.Source, v.Path)
		}
		fmt.Println("\n* = el que atiende los sitios")
		return nil
	case "remote":
		return cmdServices(env, []string{"remote", "apache"})
	case "install":
		want := "latest"
		if len(args) > 1 {
			want = args[1]
		}
		return cmdServices(env, []string{"install", "apache", want})
	case "use":
		if len(args) < 2 {
			return fmt.Errorf("uso: nido apache use <versión>, por ejemplo 2.4.69")
		}
		v, ok := env.Reg.Find(versions.Apache, args[1])
		if !ok {
			return fmt.Errorf("Apache %s no está instalado (mira 'nido apache list')", args[1])
		}
		// With Nido running, the switch also restarts Apache.
		if c, err := app.Connect(env.Home.Root); err == nil {
			if err := c.Call("POST", "/api/apache", map[string]string{"version": v.Version}, nil); err != nil {
				return err
			}
		} else {
			env.Cfg.Apache.Version = v.Version
			if err := env.SaveConfig(); err != nil {
				return err
			}
		}
		fmt.Println("Apache de los sitios:", v.Label())
		return nil
	}
	return fmt.Errorf("uso:%s", apacheUsage)
}
