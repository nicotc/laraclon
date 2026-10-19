package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/install"
)

// nodeBackend runs the Node actions through the running Nido, so its panel, its config and its Vite
// servers follow them, or, when it is not running, on a local daemon object (same code, no services).
type nodeBackend struct {
	c *app.Client
	d *app.Daemon
}

func newNodeBackend(env *app.Env) (nodeBackend, serviceOps, error) {
	if c, err := app.Connect(env.Home.Root); err == nil {
		return nodeBackend{c: c}, remoteOps{c}, nil
	}
	d, err := app.NewDaemon(env)
	if err != nil {
		return nodeBackend{}, nil, err
	}
	return nodeBackend{d: d}, localOps{d}, nil
}

func (b nodeBackend) status(refresh bool) (app.NodeStatus, error) {
	if b.c == nil {
		return b.d.NodeStatus(context.Background(), refresh), nil
	}
	var st app.NodeStatus
	q := "/api/node"
	if refresh {
		q += "?refresh=1"
	}
	return st, b.c.Call("GET", q, nil, &st)
}

func (b nodeBackend) install(want string, use bool) (app.Job, error) {
	if b.c == nil {
		return b.d.InstallNode(want, use)
	}
	var j app.Job
	return j, b.c.Call("POST", "/api/node/install", map[string]any{"version": want, "use": use}, &j)
}

func (b nodeBackend) use(version string) (string, error) {
	if b.c == nil {
		v, err := b.d.UseNode(version)
		return v.Version, err
	}
	var out map[string]string
	err := b.c.Call("POST", "/api/node/use", map[string]string{"version": version}, &out)
	return out["node"], err
}

func (b nodeBackend) uninstall(version string) error {
	if b.c == nil {
		return b.d.UninstallNode(version)
	}
	return b.c.Call("POST", "/api/node/uninstall", map[string]string{"version": version}, nil)
}

func (b nodeBackend) tool(name string, on bool) (app.Job, error) {
	if b.c == nil {
		return b.d.SetNodeTool(name, on)
	}
	var j app.Job
	return j, b.c.Call("POST", "/api/node/tool", map[string]any{"name": name, "on": on}, &j)
}

func isNodeTool(name string) bool {
	for _, t := range install.NodeTools {
		if t == name {
			return true
		}
	}
	return false
}

func cmdNode(ctx context.Context, env *app.Env, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	b, ops, err := newNodeBackend(env)
	if err != nil {
		return err
	}
	var rest []string
	use := false
	for _, a := range args[1:] {
		if a == "--use" {
			use = true
		} else {
			rest = append(rest, strings.ToLower(a))
		}
	}
	switch args[0] {
	case "list":
		st, err := b.status(false)
		if err != nil {
			return err
		}
		if len(st.Installed) == 0 {
			fmt.Println("No hay ninguna versión de Node: instala la LTS con 'nido node install lts'.")
		}
		for _, v := range st.Installed {
			mark := " "
			if v.InUse {
				mark = "*"
			}
			fmt.Printf("%s Node %-10s %-9s %s\n", mark, v.Version, v.Source, v.Path)
		}
		for _, t := range st.Tools {
			if t.Version != "" {
				fmt.Printf("  %-15s paquete global de npm\n", t.Name+" "+t.Version)
			}
		}
		printNodeUpdates(st)
		return nil
	case "remote":
		st, err := b.status(len(rest) > 0 && rest[0] == "--refresh")
		if err != nil {
			return err
		}
		if st.Error != "" {
			return fmt.Errorf("%s", st.Error)
		}
		fmt.Println("Versiones de Node en nodejs.org (la última de cada línea con soporte):")
		for _, r := range st.Lines {
			fmt.Printf("  %-10s %s\n", r.Version, r.Note)
		}
		printNodeUpdates(st)
		fmt.Println("\nOtras versiones: 'nido node install <versión>', por ejemplo 20 o 22.12.0.")
		return nil
	case "install":
		want := "lts"
		if len(rest) > 0 {
			want = rest[0]
		}
		var j app.Job
		if isNodeTool(want) {
			j, err = b.tool(want, true)
		} else {
			j, err = b.install(want, use)
		}
		if err != nil {
			return err
		}
		return followJob(ops, j)
	case "use":
		if len(rest) == 0 {
			return fmt.Errorf("uso: nido node use <versión>, por ejemplo 24")
		}
		v, err := b.use(rest[0])
		if err != nil {
			return err
		}
		fmt.Printf("Node global: %s (terminal, Composer y los servidores de Vite)\n", v)
		return nil
	case "uninstall", "remove":
		if len(rest) == 0 {
			return fmt.Errorf("uso: nido node uninstall <versión exacta> (o pnpm, yarn)")
		}
		if isNodeTool(rest[0]) {
			j, err := b.tool(rest[0], false)
			if err != nil {
				return err
			}
			return followJob(ops, j)
		}
		if err := b.uninstall(rest[0]); err != nil {
			return err
		}
		fmt.Println("Quitado: Node", strings.TrimPrefix(rest[0], "v"))
		return nil
	}
	return fmt.Errorf("subcomando de node desconocido: %s", args[0])
}

func printNodeUpdates(st app.NodeStatus) {
	for _, u := range st.Updates {
		how := fmt.Sprintf("'nido node install %s --use'", u.Version)
		if u.Installed {
			how = fmt.Sprintf("'nido node use %s' (ya está descargada)", u.Version)
		}
		switch {
		case u.Line:
			fmt.Printf("\nSalió Node %s, la nueva LTS (%s). Instálala con %s.\n", u.Version, u.LTS, how)
		case u.Security:
			fmt.Printf("\nNode %s corrige fallos de seguridad de tu versión (%s). Actualiza con %s.\n", u.Version, st.Global, how)
		default:
			fmt.Printf("\nHay una versión nueva de tu línea de Node: %s (%s).\n", u.Version, how)
		}
	}
}
