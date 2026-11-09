package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/mcp"
)

const mcpUsage = `
  nido mcp                          servidor MCP (stdio) para editores con IA: sitios, servicios, logs, dumps…
  nido mcp install | uninstall [--client claude|cursor|vscode|windsurf|codex]
                                    lo registra en el editor (sin --client, en todos los que encuentre)
  nido mcp status                   en qué editores está registrado
`

// cmdMCP serves MCP on stdin/stdout, or registers Nido's server in the AI editors.
func cmdMCP(env *app.Env, args []string) error {
	if len(args) == 0 {
		return serveMCP(env)
	}
	client := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--client" && i+1 < len(args) {
			client = strings.ToLower(args[i+1])
			i++
		} else if strings.HasPrefix(args[i], "--client=") {
			client = strings.ToLower(strings.TrimPrefix(args[i], "--client="))
		}
	}
	targets := mcpTargets(env)
	if client != "" {
		var pick []mcpTarget
		for _, t := range targets {
			if t.id == client {
				pick = append(pick, t)
			}
		}
		if len(pick) == 0 {
			return fmt.Errorf("editor desconocido %q: usa claude, cursor, vscode, windsurf o codex", client)
		}
		targets = pick
	}
	switch args[0] {
	case "status":
		for _, t := range targets {
			state := "no registrado"
			if t.present() {
				state = "registrado"
			}
			fmt.Printf("  %-10s %-14s %s\n", t.name, state, t.where)
		}
		return nil
	case "install", "uninstall":
		on := args[0] == "install"
		done := 0
		for _, t := range targets {
			if client == "" && !t.detected() {
				continue
			}
			if err := t.apply(on); err != nil {
				fmt.Printf("  %-10s %v\n", t.name, err)
				continue
			}
			done++
			if on {
				fmt.Printf("  %-10s registrado en %s\n", t.name, t.where)
			} else {
				fmt.Printf("  %-10s quitado de %s\n", t.name, t.where)
			}
		}
		if done == 0 && client == "" {
			fmt.Println("No encontré ningún editor con IA instalado. Elige uno con --client claude|cursor|vscode|windsurf|codex.")
		}
		if on && done > 0 {
			fmt.Println("Reinicia el editor (o recarga sus servidores MCP) para que vea a Nido.")
		}
		return nil
	}
	return fmt.Errorf("uso:%s", mcpUsage)
}

// mcpTarget is one editor's MCP configuration.
type mcpTarget struct {
	id, name, where string
	detected        func() bool
	present         func() bool
	apply           func(on bool) error
}

func mcpTargets(env *app.Env) []mcpTarget {
	exe := env.Home.Path("nido.exe")
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	entry := map[string]any{"command": exe, "args": []string{"mcp"}}
	vscodeEntry := map[string]any{"type": "stdio", "command": exe, "args": []string{"mcp"}}
	jsonTarget := func(id, name, file, key string, value map[string]any, marker string) mcpTarget {
		return mcpTarget{id: id, name: name, where: file,
			detected: func() bool { return dirExists(filepath.Dir(file)) || (marker != "" && dirExists(marker)) },
			present:  func() bool { return jsonHas(file, key, "nido") },
			apply:    func(on bool) error { return jsonSet(file, key, "nido", value, on) }}
	}
	claudeFile := filepath.Join(home, ".claude.json")
	codexFile := filepath.Join(home, ".codex", "config.toml")
	return []mcpTarget{
		{id: "claude", name: "Claude Code", where: claudeFile + " (con 'claude mcp add')",
			detected: func() bool { _, err := exec.LookPath("claude"); return err == nil },
			present:  func() bool { return jsonHas(claudeFile, "mcpServers", "nido") },
			apply: func(on bool) error {
				cli, err := exec.LookPath("claude")
				if err != nil {
					if on {
						return fmt.Errorf("no encuentro el comando claude; ejecuta: claude mcp add --scope user nido -- %s mcp", exe)
					}
					return fmt.Errorf("no encuentro el comando claude; ejecuta: claude mcp remove --scope user nido")
				}
				args := []string{"mcp", "add", "--scope", "user", "nido", "--", exe, "mcp"}
				if !on {
					args = []string{"mcp", "remove", "--scope", "user", "nido"}
				}
				if out, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
					return fmt.Errorf("claude %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
				}
				return nil
			}},
		jsonTarget("cursor", "Cursor", filepath.Join(home, ".cursor", "mcp.json"), "mcpServers", entry, ""),
		jsonTarget("vscode", "VS Code", filepath.Join(appData, "Code", "User", "mcp.json"), "servers", vscodeEntry, ""),
		jsonTarget("windsurf", "Windsurf", filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), "mcpServers", entry, ""),
		{id: "codex", name: "Codex", where: codexFile,
			detected: func() bool { return dirExists(filepath.Dir(codexFile)) },
			present: func() bool {
				data, _ := os.ReadFile(codexFile)
				return bytes.Contains(data, []byte("[mcp_servers.nido]"))
			},
			apply: func(on bool) error { return codexSet(codexFile, exe, on) }},
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func readJSONObject(file string) (map[string]any, error) {
	doc := map[string]any{}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s no es JSON válido (¿tiene comentarios?): edítalo a mano", file)
	}
	return doc, nil
}

func jsonHas(file, key, name string) bool {
	doc, err := readJSONObject(file)
	if err != nil {
		return false
	}
	servers, _ := doc[key].(map[string]any)
	_, ok := servers[name]
	return ok
}

// jsonSet adds or removes one server in an editor's JSON file, keeping everything else in it.
func jsonSet(file, key, name string, value map[string]any, on bool) error {
	doc, err := readJSONObject(file)
	if err != nil {
		return err
	}
	servers, _ := doc[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if on {
		servers[name] = value
	} else {
		delete(servers, name)
	}
	doc[key] = servers
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(file, append(data, '\n'))
}

// codexSet adds or removes [mcp_servers.nido] in Codex's config.toml, leaving the rest as it was.
func codexSet(file, exe string, on bool) error {
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var kept []string
	skip := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			skip = t == "[mcp_servers.nido]" || strings.HasPrefix(t, "[mcp_servers.nido.")
		}
		if !skip {
			kept = append(kept, l)
		}
	}
	text := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if on {
		quoted, _ := json.Marshal(exe) // a TOML basic string is written like a JSON one
		text += fmt.Sprintf("\n\n[mcp_servers.nido]\ncommand = %s\nargs = [\"mcp\"]\n", quoted)
	}
	return config.WriteFileAtomic(file, []byte(strings.TrimLeft(text, "\n")+"\n"))
}

// serveMCP answers an AI editor on stdin/stdout with tools that talk to the running Nido.
func serveMCP(env *app.Env) error {
	call := func(method, path string, body, out any) error {
		c, err := app.Connect(env.Home.Root)
		if err != nil {
			return fmt.Errorf("Nido no está corriendo: pide al usuario que lo abra (menú Inicio) o ejecuta 'nido run'")
		}
		return c.Call(method, path, body, out)
	}
	pretty := func(v any) (string, error) {
		data, err := json.MarshalIndent(v, "", "  ")
		return string(data), err
	}
	status := func() (app.Status, error) {
		var st app.Status
		err := call("GET", "/api/status", nil, &st)
		return st, err
	}
	findSite := func(st app.Status, name string) (app.SiteStatus, error) {
		name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), "."+st.TLD)
		for _, s := range st.Sites {
			if s.Name == name {
				return s, nil
			}
		}
		return app.SiteStatus{}, fmt.Errorf("no hay ningún sitio %s.%s", name, st.TLD)
	}
	none := mcp.Schema(nil, nil)
	siteArg := map[string]string{"site": "string:nombre del sitio, como «tienda» o «tienda.test»"}
	tools := []mcp.Tool{
		{Name: "nido_status", Description: "Estado de Nido: PHP global, Node, Apache, base de datos por defecto, proxy, servicios en marcha y problemas.", InputSchema: none,
			Call: func(map[string]any) (string, error) {
				st, err := status()
				if err != nil {
					return "", err
				}
				var b strings.Builder
				fmt.Fprintf(&b, "PHP %s · Node %s · Apache %s · BD %s (puerto %d)\nProxy en marcha: %v (%d/%d) · panel %s\n", dash(st.PHP), dash(st.Node), dash(st.Apache), dash(st.DB), st.DBPort, st.ProxyUp, st.HTTPPort, st.HTTPSPort, st.PanelURL)
				fmt.Fprintf(&b, "DNS comodín: %v · %d sitios\nServicios:\n", st.DNS.Enabled && st.DNS.Running && st.DNS.Rule, len(st.Sites))
				for _, s := range st.Services {
					fmt.Fprintf(&b, "  %s: %s\n", s.Name, s.State)
				}
				keys := make([]string, 0, len(st.Problems))
				for k := range st.Problems {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&b, "Problema %s: %s\n", k, st.Problems[k])
				}
				return b.String(), nil
			}},
		{Name: "list_sites", Description: "Lista los sitios locales: dirección, tipo (laravel, wordpress…), servidor web, PHP, carpeta y, si lo tiene, su enlace público o su rama de worktree.", InputSchema: none,
			Call: func(map[string]any) (string, error) {
				st, err := status()
				if err != nil {
					return "", err
				}
				var b strings.Builder
				for _, s := range st.Sites {
					fmt.Fprintf(&b, "%s · %s · %s · PHP %s · %s", s.URL, s.Kind, s.Server, dash(s.PHPVersion), s.Folder)
					if s.Worktree != "" {
						fmt.Fprintf(&b, " · worktree de %s (rama %s)", s.Worktree, s.Branch)
					}
					if s.Share != nil {
						fmt.Fprintf(&b, " · compartido en %s", s.Share.URL)
					}
					b.WriteString("\n")
				}
				return b.String(), nil
			}},
		{Name: "site_info", Description: "Todo lo que Nido sabe de un sitio (JSON): carpetas, servidor, PHP y Node, procesos, lo que le falta, enlace público.", InputSchema: mcp.Schema([]string{"site"}, siteArg),
			Call: func(a map[string]any) (string, error) {
				st, err := status()
				if err != nil {
					return "", err
				}
				s, err := findSite(st, mcp.Str(a, "site"))
				if err != nil {
					return "", err
				}
				return pretty(s)
			}},
		{Name: "list_services", Description: "Bases de datos y servicios instalados (MySQL, MariaDB, PostgreSQL, MongoDB, Redis, Mailpit, Meilisearch) con versión, puerto y estado.", InputSchema: none,
			Call: func(map[string]any) (string, error) {
				st, err := status()
				if err != nil {
					return "", err
				}
				var b strings.Builder
				for _, g := range st.Databases {
					for _, i := range g.Instances {
						def := ""
						if i.Default {
							def = " (por defecto)"
						}
						fmt.Fprintf(&b, "%s %s · clave %s · puerto %d · %s%s\n", g.Name, i.Version, i.Key, i.Port, i.State, def)
					}
				}
				if b.Len() == 0 {
					return "No hay servicios instalados.", nil
				}
				return b.String(), nil
			}},
		{Name: "service", Description: "Inicia o detiene un servicio por su clave (la de list_services, p. ej. mysql-8.4).", InputSchema: mcp.Schema([]string{"key", "action"}, map[string]string{"key": "string:clave del servicio", "action": "string:start o stop"}),
			Call: func(a map[string]any) (string, error) {
				action := mcp.Str(a, "action")
				if action != "start" && action != "stop" {
					return "", fmt.Errorf("action debe ser start o stop")
				}
				if err := call("POST", "/api/services/"+action, map[string]string{"key": mcp.Str(a, "key")}, nil); err != nil {
					return "", err
				}
				return "Hecho: " + action + " " + mcp.Str(a, "key"), nil
			}},
		{Name: "recent_dumps", Description: "Los últimos dump() y dd() de los proyectos, por petición o comando, con archivo y línea.", InputSchema: mcp.Schema(nil, map[string]string{"limit": "integer:cuántas peticiones (10 por defecto)", "site": "string:solo las de este sitio"}),
			Call: func(a map[string]any) (string, error) {
				var v app.DumpsView
				if err := call("GET", "/api/dumps", nil, &v); err != nil {
					return "", err
				}
				if !v.On {
					return "La captura de dumps está apagada (pestaña Dumps del panel o [dumps] off en nido.toml).", nil
				}
				site := strings.ToLower(mcp.Str(a, "site"))
				limit := mcp.Int(a, "limit", 10)
				var b strings.Builder
				n := 0
				for _, g := range v.Groups {
					if site != "" && !strings.HasPrefix(strings.ToLower(g.Host), site) {
						continue
					}
					if n == limit {
						break
					}
					n++
					fmt.Fprintf(&b, "== %s %s %s (%s)\n", g.Method, g.URI, g.Host, g.Started.Format("15:04:05"))
					for _, d := range g.Dumps {
						fmt.Fprintf(&b, "-- %s %s:%d (%s)\n%s\n", d.Kind, d.File, d.Line, d.Type, d.Text)
					}
				}
				if n == 0 {
					return "No hay dumps todavía.", nil
				}
				return b.String(), nil
			}},
		{Name: "read_log", Description: "Últimas líneas de un log: «laravel» (storage/logs/laravel.log de un sitio), «php», «nido», o el de un proceso del sitio (vite, queue, schedule, reverb, share).", InputSchema: mcp.Schema([]string{"log"}, map[string]string{"log": "string:laravel, php, nido, vite, queue, schedule, reverb o share", "site": "string:el sitio (para laravel y los procesos)", "lines": "integer:cuántas líneas (80 por defecto)"}),
			Call: func(a map[string]any) (string, error) {
				lines := mcp.Int(a, "lines", 80)
				if lines <= 0 || lines > 2000 {
					lines = 80
				}
				name := mcp.Str(a, "log")
				var file string
				switch name {
				case "nido":
					file = env.Home.Logs("nido.log")
				case "php":
					st, err := status()
					if err != nil {
						return "", err
					}
					file = env.Home.Logs("php-" + st.PHP + ".log")
				case "laravel", "vite", "queue", "schedule", "reverb", "share":
					st, err := status()
					if err != nil {
						return "", err
					}
					s, err := findSite(st, mcp.Str(a, "site"))
					if err != nil {
						return "", err
					}
					switch name {
					case "laravel":
						file = filepath.Join(s.Folder, "storage", "logs", "laravel.log")
					case "share":
						file = env.Home.Logs("share-" + s.Name + ".log")
					default:
						file = env.Home.Logs(s.Name + "-" + name + ".log")
					}
				default:
					return "", fmt.Errorf("log desconocido %q", name)
				}
				data, err := os.ReadFile(file)
				if err != nil {
					return "", fmt.Errorf("no se pudo leer %s: %w", file, err)
				}
				all := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
				if len(all) > lines {
					all = all[len(all)-lines:]
				}
				return file + "\n" + strings.Join(all, "\n"), nil
			}},
		{Name: "set_site_php", Description: "Fija la versión de PHP de un sitio (una instalada, p. ej. 8.3) o lo devuelve al PHP global con «global».", InputSchema: mcp.Schema([]string{"site", "version"}, map[string]string{"site": siteArg["site"], "version": "string:versión instalada o global"}),
			Call: func(a map[string]any) (string, error) {
				st, err := status()
				if err != nil {
					return "", err
				}
				s, err := findSite(st, mcp.Str(a, "site"))
				if err != nil {
					return "", err
				}
				v := mcp.Str(a, "version")
				if strings.EqualFold(v, "global") {
					v = ""
				}
				if err := call("POST", "/api/sites/php", map[string]string{"site": s.Name, "php": v}, nil); err != nil {
					return "", err
				}
				return "PHP de " + s.Host + ": " + dash(v) + " (— es el global)", nil
			}},
		{Name: "share_site", Description: "Abre un enlace público temporal (https://….trycloudflare.com) de un sitio, o lo cierra con stop=true.", InputSchema: mcp.Schema([]string{"site"}, map[string]string{"site": siteArg["site"], "hours": "integer:horas (8 por defecto, máximo 72)", "stop": "boolean:true para cerrarlo"}),
			Call: func(a map[string]any) (string, error) {
				if mcp.Bool(a, "stop", false) {
					return "Enlace cerrado.", call("POST", "/api/share", map[string]any{"site": mcp.Str(a, "site"), "on": false}, nil)
				}
				var sh app.Share
				if err := call("POST", "/api/share", map[string]any{"site": mcp.Str(a, "site"), "on": true, "hours": mcp.Int(a, "hours", 0)}, &sh); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s compartido en %s hasta las %s", sh.Site, sh.URL, sh.Expires.Format("15:04")), nil
			}},
		{Name: "create_worktree", Description: "Crea un worktree de Git de un sitio para una rama, servido en <rama>.<sitio>.test, y espera a que esté listo.", InputSchema: mcp.Schema([]string{"site", "branch"}, map[string]string{"site": siteArg["site"], "branch": "string:la rama (existente o nueva)", "db": "string:compartida, copia o nueva"}),
			Call: func(a map[string]any) (string, error) {
				var j app.Job
				if err := call("POST", "/api/worktrees", map[string]string{"site": mcp.Str(a, "site"), "branch": mcp.Str(a, "branch"), "db": mcp.Str(a, "db")}, &j); err != nil {
					return "", err
				}
				c, err := app.Connect(env.Home.Root)
				if err != nil {
					return "", err
				}
				final, err := waitJob(remoteOps{c}, j)
				if err != nil {
					return "", err
				}
				if final.Error != "" {
					return "", errors.New(final.Error)
				}
				return final.Note, nil
			}},
	}
	srv := &mcp.Server{Name: "nido", Version: app.Version, Tools: tools,
		Instructions: "Nido es el entorno de desarrollo local de este Windows: sirve cada proyecto en https://<carpeta>.test con PHP, Apache/Nginx/FrankenPHP y sus bases de datos. Usa list_sites para saber qué proyectos hay y en qué carpeta, read_log y recent_dumps para depurar, y nido_status cuando algo no responde."}
	return srv.Serve(os.Stdin, os.Stdout)
}
