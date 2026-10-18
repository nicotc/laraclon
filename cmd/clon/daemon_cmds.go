package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/ca"
	"github.com/nicotc/laraclon/internal/hosts"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/winx"
)

const daemonUsage = `
  clon run [--foreground]           inicia clon (icono en la bandeja); --foreground lo deja en esta consola
  clon start | stop | restart       inicia, detiene o reinicia los servicios
  clon status                       estado de servicios y sitios
  clon quit                         cierra clon (detiene todo)
  clon sites                        lista los sitios .test

  clon ca status | trust | untrust  CA local para HTTPS (trust pide confirmación a Windows)
  clon hosts status | sync          archivo hosts (sync pide permiso de administrador)
  clon path add | remove            añade o quita shims\ del PATH de tu usuario
`

// cmdRun starts the daemon: in the background with the tray icon (clonw.exe), or in this console.
func cmdRun(ctx context.Context, env *app.Env, args []string) error {
	if _, err := app.Connect(env.Home.Root); err == nil {
		fmt.Println("clon ya está corriendo")
		return nil
	}
	foreground := len(args) > 0 && args[0] == "--foreground"
	if !foreground {
		exe := env.Home.Path("clonw.exe")
		if _, err := os.Stat(exe); err != nil {
			return fmt.Errorf("falta %s; usa 'clon run --foreground'", exe)
		}
		if err := startDetached(exe); err != nil {
			return err
		}
		for i := 0; i < 50; i++ {
			time.Sleep(200 * time.Millisecond)
			if _, err := app.Connect(env.Home.Root); err == nil {
				fmt.Println("clon iniciado (icono en la bandeja del sistema)")
				return nil
			}
		}
		return fmt.Errorf("clon no respondió; mira %s", env.Home.Logs("clon.log"))
	}
	d, err := app.NewDaemon(env)
	if err != nil {
		return err
	}
	fmt.Println("clon en primer plano. Panel:", d.PanelURL(), "· Ctrl+C para salir")
	return d.Run(ctx)
}

// startDetached launches the tray program so it outlives this console. Some terminals and IDEs
// run commands inside a job object that kills children on exit; breaking away avoids that when
// the job allows it.
func startDetached(exe string) error {
	const createBreakawayFromJob = 0x01000000
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | createBreakawayFromJob}
	if err := cmd.Start(); err == nil {
		return cmd.Process.Release()
	}
	cmd = exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func withDaemon(env *app.Env, fn func(*app.Client) error) error {
	c, err := app.Connect(env.Home.Root)
	if err != nil {
		return fmt.Errorf("clon no está corriendo (inícialo con 'clon run')")
	}
	return fn(c)
}

func cmdService(env *app.Env, action string) error {
	return withDaemon(env, func(c *app.Client) error {
		var st app.Status
		if err := c.Call("POST", "/api/"+action, nil, &st); err != nil {
			return err
		}
		printStatus(st)
		return nil
	})
}

func cmdQuit(env *app.Env) error {
	return withDaemon(env, func(c *app.Client) error {
		if err := c.Call("POST", "/api/shutdown", nil, nil); err != nil {
			return err
		}
		fmt.Println("clon se está cerrando y detiene los servicios")
		return nil
	})
}

func cmdStatus(env *app.Env) error {
	return withDaemon(env, func(c *app.Client) error {
		var st app.Status
		if err := c.Call("GET", "/api/status", nil, &st); err != nil {
			return err
		}
		printStatus(st)
		return nil
	})
}

func printStatus(st app.Status) {
	fmt.Printf("PHP %s · Node %s · Apache %s · MySQL %s (puerto %d)\n", dash(st.PHP), dash(st.Node), dash(st.Apache), dash(st.MySQL), st.MySQLPort)
	proxy := "detenido"
	if st.ProxyUp {
		proxy = fmt.Sprintf("en marcha en %d/%d", st.HTTPPort, st.HTTPSPort)
	}
	fmt.Println("Proxy HTTPS:", proxy, "· panel:", st.PanelURL)
	fmt.Println("\nServicios:")
	for _, s := range st.Services {
		line := fmt.Sprintf("  %-16s %-11s", s.Name, s.State)
		if s.PID != 0 {
			line += fmt.Sprintf(" PID %-6d", s.PID)
		}
		if s.Port != 0 {
			line += fmt.Sprintf(" puerto %d", s.Port)
		}
		fmt.Println(line)
		if s.LastError != "" && s.State != "en marcha" {
			fmt.Println("    ", firstLine(s.LastError))
		}
	}
	fmt.Printf("\nSitios: %d\n", len(st.Sites))
	for k, v := range st.Problems {
		fmt.Printf("  ! %s: %s\n", k, v)
	}
	if !st.CATrusted {
		fmt.Println("  ! Windows aún no confía en la CA local: 'clon ca trust'")
	}
	if len(st.HostsMissing) > 0 {
		fmt.Printf("  ! %d sitio(s) sin entrada en hosts: 'clon hosts sync'\n", len(st.HostsMissing))
	}
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func cmdSites(env *app.Env) error {
	var list []app.SiteStatus
	var warns []sites.Warning
	if c, err := app.Connect(env.Home.Root); err == nil {
		var st app.Status
		if err := c.Call("GET", "/api/status", nil, &st); err != nil {
			return err
		}
		list, warns = st.Sites, st.Warnings
	} else {
		found, w := sites.Scan(env.Cfg)
		store, _ := sites.LoadPorts(env.Home.State("site-ports.json"))
		for _, s := range found {
			if store != nil {
				s.Port = store.Ports[s.Name]
			}
			list = append(list, app.SiteStatus{Site: s, URL: "https://" + s.Host + "/"})
		}
		warns = w
	}
	for _, s := range list {
		fmt.Printf("  %-34s %-9s %s\n", s.URL, s.Kind, s.DocRoot)
	}
	for _, w := range warns {
		fmt.Printf("  omitida: %s (%s)\n", w.Folder, w.Reason)
	}
	fmt.Printf("%d sitios\n", len(list))
	return nil
}

func cmdCA(env *app.Env, args []string) error {
	authority, _, err := ca.LoadOrCreate(env.Home.SSL())
	if err != nil {
		return err
	}
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "status":
		fmt.Println("Certificado de la CA:", authority.CertPath())
		if authority.Trusted() {
			fmt.Println("Windows confía en ella (Chrome, Edge y Firefox la usan).")
		} else {
			fmt.Println("Windows todavía no confía en ella. Ejecuta 'clon ca trust'.")
		}
		return nil
	case "trust":
		fmt.Println("Windows te pedirá confirmar que confías en la CA local de clon (solo para tu usuario)…")
		if err := authority.Trust(); err != nil {
			return err
		}
		fmt.Println("Listo: la CA local está en los certificados de confianza de tu usuario.")
		return nil
	case "untrust":
		return authority.Untrust()
	}
	return fmt.Errorf("uso: clon ca status | trust | untrust")
}

func cmdHosts(env *app.Env, args []string) error {
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	content, err := os.ReadFile(hosts.Path())
	if err != nil {
		return err
	}
	found, _ := sites.Scan(env.Cfg)
	var names []string
	for _, s := range found {
		names = append(names, s.Host)
		names = append(names, s.Aliases...)
	}
	switch action {
	case "status":
		missing := hosts.Missing(string(content), names)
		if len(missing) == 0 {
			fmt.Printf("Los %d sitios ya se resuelven con el archivo hosts.\n", len(names))
			return nil
		}
		fmt.Printf("%d sitio(s) sin entrada en hosts:\n", len(missing))
		for _, n := range missing {
			fmt.Println("  ", n)
		}
		fmt.Println("Ejecuta 'clon hosts sync' (Windows pedirá permiso de administrador).")
		return nil
	case "sync":
		if hosts.InSync(string(content), names) {
			fmt.Println("El bloque de clon en hosts ya está al día.")
			return nil
		}
		list := env.Home.Run("hosts-names.txt")
		if err := os.WriteFile(list, []byte(strings.Join(names, "\r\n")), 0o644); err != nil {
			return err
		}
		fmt.Println("Windows te pedirá permiso de administrador para actualizar el archivo hosts…")
		if err := hosts.RequestElevated(env.Home.Path("clon.exe"), list); err != nil {
			return err
		}
		fmt.Printf("Archivo hosts actualizado (%d nombres). Copia previa en %s.clon.bak\n", len(names), hosts.Path())
		return nil
	}
	return fmt.Errorf("uso: clon hosts status | sync")
}

func cmdPath(env *app.Env, args []string) error {
	if len(args) == 0 || (args[0] != "add" && args[0] != "remove") {
		return fmt.Errorf("uso: clon path add | remove")
	}
	dirs := []string{env.Home.Shims(), env.NPMPrefix()}
	if args[0] == "add" {
		if err := winx.AddToUserPath(dirs...); err != nil {
			return err
		}
		fmt.Println("Añadido al PATH de tu usuario (abre una terminal nueva para usarlo):")
	} else {
		if err := winx.RemoveFromUserPath(dirs...); err != nil {
			return err
		}
		fmt.Println("Quitado del PATH de tu usuario:")
	}
	for _, d := range dirs {
		fmt.Println("  ", d)
	}
	return nil
}
