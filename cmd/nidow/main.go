// nidow is Nido with its tray icon. It runs in the background (no console window) and owns the
// services; the command line (nido.exe) talks to it.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/winx"
)

func main() {
	env, err := app.Open()
	if err != nil {
		fatal(err)
	}
	if logf, err := os.OpenFile(env.Home.Logs("nido.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(logf)
	}

	// A second start just opens the panel of the running instance.
	if c, err := app.Connect(env.Home.Root); err == nil {
		var st app.Status
		if c.Call("GET", "/api/status", nil, &st) == nil {
			winx.OpenAppWindow(st.PanelURL)
		}
		return
	}

	d, err := app.NewDaemon(env)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &tray{d: d}
	d.OnChange = t.requestRefresh

	systray.Run(func() {
		systray.SetIcon(iconStopped)
		systray.SetTooltip("Nido")
		systray.SetOnTapped(func() { winx.OpenAppWindow(d.PanelURL()) })
		t.build()
		go func() {
			if err := d.Run(ctx); err != nil {
				log.Println("Nido:", err)
			}
			systray.Quit()
		}()
		go func() {
			for range time.Tick(15 * time.Second) {
				t.requestRefresh()
			}
		}()
	}, func() {
		cancel()
		d.Shutdown()
	})
}

const (
	siteSlots    = 40
	phpSlots     = 16
	problemSlots = 4
)

// tray builds the menu once and afterwards only renames, checks, shows or hides items. Rebuilding
// menus at run time is fragile with the Windows tray API, so lists use pre-allocated slots.
type tray struct {
	d *app.Daemon

	mu        sync.Mutex
	pending   bool
	panelURL  string
	siteURLs  [siteSlots]string
	phpValues [phpSlots]string

	sites, php, start, stop, trust, hostsItem *systray.MenuItem
	siteItems                                 [siteSlots]*systray.MenuItem
	moreSites                                 *systray.MenuItem
	phpItems                                  [phpSlots]*systray.MenuItem
	problems                                  [problemSlots]*systray.MenuItem
}

// onClick runs fn for every real click. Closed channels (application exit) end the loop.
func onClick(item *systray.MenuItem, fn func()) {
	go func() {
		for range item.ClickedCh {
			go fn()
		}
	}()
}

func (t *tray) build() {
	onClick(systray.AddMenuItem("Abrir panel", ""), func() { winx.OpenAppWindow(t.d.PanelURL()) })
	onClick(systray.AddMenuItem("Salud del entorno", "PHP, bases de datos y sitios: qué responde y qué no"),
		func() { winx.OpenURL(t.d.HealthURL()) })

	t.sites = systray.AddMenuItem("Sitios", "")
	for i := range t.siteItems {
		i := i
		t.siteItems[i] = t.sites.AddSubMenuItem("", "")
		t.siteItems[i].Hide()
		onClick(t.siteItems[i], func() {
			t.mu.Lock()
			url := t.siteURLs[i]
			t.mu.Unlock()
			if url != "" {
				winx.OpenURL(url)
			}
		})
	}
	t.moreSites = t.sites.AddSubMenuItem("Más sitios en el panel…", "")
	t.moreSites.Hide()
	onClick(t.moreSites, func() { winx.OpenAppWindow(t.d.PanelURL()) })

	t.php = systray.AddMenuItem("PHP", "Versión global de PHP")
	for i := range t.phpItems {
		i := i
		t.phpItems[i] = t.php.AddSubMenuItemCheckbox("", "", false)
		t.phpItems[i].Hide()
		onClick(t.phpItems[i], func() {
			t.mu.Lock()
			v := t.phpValues[i]
			t.mu.Unlock()
			if v == "" {
				return
			}
			if _, err := t.d.UsePHP(v); err != nil {
				log.Println("php:", err)
			}
		})
	}

	systray.AddSeparator()
	t.start = systray.AddMenuItem("Iniciar todo", "")
	onClick(t.start, func() { t.d.StartAll() })
	t.stop = systray.AddMenuItem("Detener todo", "")
	onClick(t.stop, func() { t.d.StopAll() })
	t.trust = systray.AddMenuItem("Confiar en la CA local…", "Windows pedirá confirmación")
	t.trust.Hide()
	onClick(t.trust, func() {
		if err := t.d.TrustCA(); err != nil {
			log.Println("ca:", err)
		}
	})
	t.hostsItem = systray.AddMenuItem("Actualizar hosts…", "Windows pedirá permiso de administrador")
	t.hostsItem.Hide()
	onClick(t.hostsItem, func() {
		if err := t.d.SyncHosts(); err != nil {
			log.Println("hosts:", err)
		}
	})
	for i := range t.problems {
		t.problems[i] = systray.AddMenuItem("", "")
		t.problems[i].Disable()
		t.problems[i].Hide()
	}
	systray.AddSeparator()
	onClick(systray.AddMenuItem("Salir", "Detiene todos los servicios"), func() { t.d.Shutdown() })
	t.refresh()
}

func (t *tray) requestRefresh() {
	t.mu.Lock()
	if t.pending {
		t.mu.Unlock()
		return
	}
	t.pending = true
	t.mu.Unlock()
	go func() {
		time.Sleep(300 * time.Millisecond) // coalesce bursts of state changes
		t.refresh()
	}()
}

func (t *tray) refresh() {
	t.mu.Lock()
	t.pending = false
	t.mu.Unlock()
	st := t.d.Status()

	running := 0
	for _, s := range st.Services {
		if s.State == "en marcha" {
			running++
		}
	}
	if running > 0 && st.ProxyUp {
		systray.SetIcon(iconRunning)
	} else {
		systray.SetIcon(iconStopped)
	}
	systray.SetTooltip(fmt.Sprintf("Nido · PHP %s · %d sitios", st.PHP, len(st.Sites)))

	t.mu.Lock()
	defer t.mu.Unlock()
	t.sites.SetTitle(fmt.Sprintf("Sitios (%d)", len(st.Sites)))
	for i, item := range t.siteItems {
		if i < len(st.Sites) {
			t.siteURLs[i] = st.Sites[i].URL
			item.SetTitle(st.Sites[i].Host)
			item.Show()
		} else {
			t.siteURLs[i] = ""
			item.Hide()
		}
	}
	if len(st.Sites) > siteSlots {
		t.moreSites.Show()
	} else {
		t.moreSites.Hide()
	}

	t.php.SetTitle("PHP " + st.PHP)
	for i, item := range t.phpItems {
		if i < len(st.PHPVersions) {
			v := st.PHPVersions[i]
			t.phpValues[i] = v
			item.SetTitle("PHP " + v)
			if v == st.PHP {
				item.Check()
			} else {
				item.Uncheck()
			}
			item.Show()
		} else {
			t.phpValues[i] = ""
			item.Hide()
		}
	}

	if st.CATrusted {
		t.trust.Hide()
	} else {
		t.trust.Show()
	}
	if len(st.HostsMissing) > 0 {
		t.hostsItem.SetTitle(fmt.Sprintf("Actualizar hosts (%d sitios)…", len(st.HostsMissing)))
		t.hostsItem.Show()
	} else {
		t.hostsItem.Hide()
	}
	i := 0
	for what, msg := range st.Problems {
		if i >= problemSlots {
			break
		}
		t.problems[i].SetTitle("⚠ " + what + ": " + shorten(msg, 60))
		t.problems[i].Show()
		i++
	}
	for ; i < problemSlots; i++ {
		t.problems[i].Hide()
	}
}

func shorten(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func fatal(err error) {
	winx.MessageBox("Nido", err.Error())
	os.Exit(1)
}
