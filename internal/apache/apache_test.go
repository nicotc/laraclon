package apache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestWriteSendsEachSiteToItsPHPPool(t *testing.T) {
	root := t.TempDir() // a fake Apache with the modules Nido needs
	for _, m := range requiredModules {
		p := filepath.Join(root, "modules", "mod_"+m+".so")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	conf, err := Write(Options{
		Home:      paths.Home{Root: t.TempDir()},
		Version:   versions.Version{Component: versions.Apache, Version: "2.4.69", Path: root},
		AdminPort: 20000,
		Pools:     []Pool{{Name: GlobalPool, Ports: []int{9100, 9101}}, {Name: "php-7-2-0", Ports: []int{9200, 9201}}},
		Sites: []Site{
			{Host: "app.test", DocRoot: `E:\www\app\public`, Port: 20001, HTTPS: true},
			{Host: "viejo.test", DocRoot: `E:\www\viejo`, Port: 20002, HTTPS: true, Pool: "php-7-2-0"},
			{Host: "raro.test", DocRoot: `E:\www\raro`, Port: 20003, Pool: "php-5-6-40"}, // no such pool
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`<Proxy "balancer://php">`, `BalancerMember "fcgi://127.0.0.1:9101" retry=1`,
		`<Proxy "balancer://php-7-2-0">`, `BalancerMember "fcgi://127.0.0.1:9200" retry=1`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("httpd.conf no contiene %q", want)
		}
	}
	section := func(host string) string {
		s := text[strings.Index(text, "# "+host):]
		return s[:strings.Index(s, "</VirtualHost>")]
	}
	for host, pool := range map[string]string{"app.test": "php", "viejo.test": "php-7-2-0", "raro.test": "php"} {
		if !strings.Contains(section(host), `SetHandler "proxy:balancer://`+pool+`/"`) {
			t.Errorf("%s debe ir al grupo %s", host, pool)
		}
	}
	if _, err := Write(Options{Home: paths.Home{Root: t.TempDir()}, Version: versions.Version{Path: root}}); err == nil {
		t.Error("sin el grupo de PHP global, Write debe fallar")
	}
}
