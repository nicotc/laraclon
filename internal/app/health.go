package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Nido watches its own health without a browser: every minute it asks the health page (the PHP behind
// https://salud.localhost, which tries every running service and the database of each site the way a
// project would) and keeps the answer for the panel's «Salud» tab and the tray.

const (
	healthFirst = 20 * time.Second // after Nido starts: the services are coming up
	healthEvery = time.Minute
)

// HealthReport is the health page's answer (its JSON, with the page's own Spanish keys).
type HealthReport struct {
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"` // the page itself could not answer
	Checked  time.Time       `json:"checked"`
	Summary  HealthSummary   `json:"resumen"`
	PHP      HealthPHP       `json:"php"`
	Services []HealthService `json:"servicios"`
	Sites    []HealthSite    `json:"sitios"`
}

type HealthSummary struct {
	OK    int `json:"ok"`
	Total int `json:"total"`
}

type HealthPHP struct {
	Version    string          `json:"version"`
	Arch       string          `json:"arquitectura"`
	Ini        string          `json:"ini"`
	Memory     string          `json:"memoria"`
	Extensions map[string]bool `json:"extensiones"`
	Others     []string        `json:"otras"`
}

type HealthService struct {
	Name    string `json:"servicio"`
	Key     string `json:"clave"`
	Address string `json:"direccion"`
	State   string `json:"estado"` // ok, fallo, apagado, iniciando
	Detail  string `json:"detalle"`
}

type HealthSite struct {
	Site   string `json:"sitio"`
	URL    string `json:"url"`
	PHP    string `json:"php"`
	Server string `json:"servidor"`
	DB     string `json:"bd"`
	State  string `json:"estado"` // ok, fallo, sin-bd
	Detail string `json:"detalle"`
}

// watchHealth checks the health once the services had time to start, and then every minute.
func (d *Daemon) watchHealth(ctx context.Context) {
	wait := time.NewTimer(healthFirst)
	defer wait.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wait.C:
		}
		d.checkHealth(ctx)
		wait.Reset(healthEvery)
	}
}

// CheckHealth checks right away (the panel's «Comprobar ahora») and returns the report.
func (d *Daemon) CheckHealth() *HealthReport {
	d.checkHealth(context.Background())
	return d.healthReport()
}

func (d *Daemon) healthReport() *HealthReport {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.health == nil {
		return nil
	}
	h := *d.health
	return &h
}

func (d *Daemon) checkHealth(ctx context.Context) {
	d.mu.Lock()
	on := d.servicesOn
	d.mu.Unlock()
	if !on || d.sup.PID(saludService) == 0 {
		return // with the services stopped there is nothing to check
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	rep := HealthReport{}
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/?formato=json", saludPort), nil)
	if err == nil {
		var resp *http.Response
		if resp, err = http.DefaultClient.Do(req); err == nil {
			err = json.NewDecoder(resp.Body).Decode(&rep) // 200 and 503 both carry the report
			resp.Body.Close()
		}
	}
	if err != nil {
		rep = HealthReport{Error: "la página de salud no respondió: " + err.Error()}
	}
	rep.Checked = time.Now()
	d.mu.Lock()
	prev := d.health
	d.health = &rep
	d.mu.Unlock()
	if prev == nil || prev.OK != rep.OK || prev.Summary != rep.Summary || prev.Error != rep.Error {
		d.changed() // the panel and the tray follow
	}
}
