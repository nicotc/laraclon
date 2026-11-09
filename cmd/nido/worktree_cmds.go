package main

import (
	"fmt"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
)

const worktreeUsage = `
  nido worktree add <sitio> <rama> [--bd compartida|copia|nueva]
                                    worktree de Git servido en <rama>.<sitio>.test, con su .env y APP_KEY
  nido worktree list [sitio]        worktrees y sus carpetas
  nido worktree remove <rama>.<sitio>
                                    lo quita de Git y de Nido (su base de datos propia se conserva)
`

func cmdWorktree(env *app.Env, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	return withDaemon(env, func(c *app.Client) error {
		switch args[0] {
		case "list", "lista":
			site := ""
			if len(args) > 1 {
				site = args[1]
			}
			var list []app.WorktreeInfo
			if err := c.Call("GET", "/api/worktrees?site="+site, nil, &list); err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("No hay worktrees. Crea uno con 'nido worktree add <sitio> <rama>'.")
			}
			for _, w := range list {
				db := "base de datos compartida"
				if w.DB != "" {
					db = "base de datos " + w.DB
				}
				fmt.Printf("  https://%-34s rama %-20s %s · %s\n", w.Host+"/", w.Branch, db, w.Folder)
			}
			return nil
		case "add", "new", "nuevo":
			if len(args) < 3 {
				return fmt.Errorf("uso: nido worktree add <sitio> <rama> [--bd compartida|copia|nueva]")
			}
			req := map[string]string{"site": args[1], "branch": args[2]}
			for i := 3; i < len(args); i++ {
				switch {
				case (args[i] == "--bd" || args[i] == "--db") && i+1 < len(args):
					req["db"] = args[i+1]
					i++
				case strings.HasPrefix(args[i], "--bd="), strings.HasPrefix(args[i], "--db="):
					req["db"] = args[i][5:]
				default:
					return fmt.Errorf("opción desconocida %q", args[i])
				}
			}
			var j app.Job
			if err := c.Call("POST", "/api/worktrees", req, &j); err != nil {
				return err
			}
			return followJob(remoteOps{c}, j)
		case "remove", "rm", "quitar":
			if len(args) != 2 {
				return fmt.Errorf("uso: nido worktree remove <rama>.<sitio>")
			}
			var out map[string]string
			if err := c.Call("POST", "/api/worktrees/remove", map[string]string{"name": args[1]}, &out); err != nil {
				return err
			}
			fmt.Println("Worktree quitado:", args[1])
			if out["note"] != "" {
				fmt.Println(out["note"])
			}
			return nil
		}
		return fmt.Errorf("uso:%s", worktreeUsage)
	})
}
