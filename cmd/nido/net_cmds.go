package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/dns"
)

const netUsage = `
  nido dns status | on | off        DNS comodín: todo *.test (y *.sitio.test) sin tocar hosts (pide permiso)
  nido share <sitio> [--horas 8] [--clave X]
                                    enlace público temporal https://….trycloudflare.com (cloudflared)
  nido share <sitio> --stop         cierra el enlace; 'nido share list' muestra los abiertos
`

// cmdDNS turns the wildcard DNS of the local domain on or off.
func cmdDNS(env *app.Env, args []string) error {
	action := "status"
	if len(args) > 0 {
		action = strings.ToLower(args[0])
	}
	switch action {
	case "status":
		if c, err := app.Connect(env.Home.Root); err == nil {
			var st app.Status
			if err := c.Call("GET", "/api/status", nil, &st); err != nil {
				return err
			}
			printDNS(env, st.DNS)
			return nil
		}
		printDNS(env, app.DNSStatus{Enabled: env.Cfg.DNS.Enabled, Rule: dns.RuleInstalled(env.Cfg.TLD)})
		return nil
	case "on", "off":
		on := action == "on"
		fmt.Println("Windows te pedirá permiso de administrador para cambiar la resolución de ." + env.Cfg.TLD + "…")
		return withDaemon(env, func(c *app.Client) error {
			var st app.Status
			if err := c.Call("POST", "/api/dns", map[string]bool{"on": on}, &st); err != nil {
				return err
			}
			printDNS(env, st.DNS)
			return nil
		})
	}
	return fmt.Errorf("uso: nido dns status | on | off")
}

func printDNS(env *app.Env, st app.DNSStatus) {
	tld := env.Cfg.TLD
	switch {
	case !st.Enabled:
		fmt.Printf("DNS comodín apagado: los sitios .%s se resuelven con el archivo hosts ('nido dns on' lo enciende).\n", tld)
	case st.Running && st.Rule:
		fmt.Printf("DNS comodín en marcha: cualquier nombre .%s (también api.tienda.%s) llega a este equipo, sin el archivo hosts.\n", tld, tld)
	case !st.Rule:
		fmt.Printf("El DNS de Nido está encendido, pero Windows todavía no le manda .%s: repite 'nido dns on' y acepta el permiso.\n", tld)
	default:
		fmt.Println("El DNS de Nido está encendido, pero no está respondiendo en 127.0.0.1:53 (¿otro programa usa el puerto 53?).")
	}
}

// cmdShare opens, closes or lists the public links of the sites.
func cmdShare(env *app.Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("uso: nido share <sitio> [--horas 8] [--clave X] | nido share <sitio> --stop | nido share list")
	}
	return withDaemon(env, func(c *app.Client) error {
		if args[0] == "list" || args[0] == "lista" {
			var list []app.Share
			if err := c.Call("GET", "/api/shares", nil, &list); err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("No hay ningún sitio compartido.")
			}
			for _, s := range list {
				lock := ""
				if s.Password {
					lock = " · con clave"
				}
				fmt.Printf("  %-28s %s (hasta las %s%s)\n", s.Site, s.URL, s.Expires.Format("15:04"), lock)
			}
			return nil
		}
		site := args[0]
		req := map[string]any{"site": site, "on": true}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--stop", "--parar":
				req["on"] = false
			case "--horas", "--hours":
				if i+1 >= len(args) {
					return fmt.Errorf("--horas necesita un número")
				}
				h, err := strconv.Atoi(args[i+1])
				if err != nil {
					return fmt.Errorf("--horas necesita un número: %w", err)
				}
				req["hours"] = h
				i++
			case "--clave", "--password":
				if i+1 >= len(args) {
					return fmt.Errorf("--clave necesita la clave")
				}
				req["password"] = args[i+1]
				i++
			default:
				return fmt.Errorf("opción desconocida %q", args[i])
			}
		}
		if req["on"] == false {
			if err := c.Call("POST", "/api/share", req, nil); err != nil {
				return err
			}
			fmt.Println("Enlace cerrado:", site)
			return nil
		}
		fmt.Println("Abriendo un enlace público (la primera vez Nido descarga cloudflared)…")
		var sh app.Share
		if err := c.Call("POST", "/api/share", req, &sh); err != nil {
			return err
		}
		fmt.Println("Compartido:", sh.Site)
		fmt.Println("  ", sh.URL)
		fmt.Printf("Dura hasta las %s (%s). Ciérralo antes con 'nido share %s --stop'.\n",
			sh.Expires.Format("15:04"), time.Until(sh.Expires).Round(time.Minute), site)
		if sh.Password {
			fmt.Println("Pide la clave (cualquier usuario).")
		}
		fmt.Println("Si el sitio usa Vite, compila el frontend (npm run build): desde fuera no se llega al servidor de Vite.")
		return nil
	})
}
