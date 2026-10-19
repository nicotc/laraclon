package nginx

import (
	"os"
	"strings"
	"testing"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestWriteGeneratesOneServerPerSite(t *testing.T) {
	home := paths.Home{Root: t.TempDir()}
	conf, err := Write(Options{
		Home:      home,
		Version:   versions.Version{Component: versions.Nginx, Version: "1.30.6", Path: `C:\nido\bin\nginx\1.30.6`},
		AdminPort: 30000,
		Pools:     []Pool{{Name: GlobalPool, Ports: []int{9100, 9101}}, {Name: "php-7-2-0", Ports: []int{9200, 9201}}},
		Sites: []Site{
			{Host: "tienda.test", Aliases: []string{"admin.tienda.test"}, DocRoot: `E:\www\mi tienda\public`, Port: 30001, HTTPS: true, PublicPort: 443},
			{Host: "viejo.test", DocRoot: `E:\www\viejo`, Port: 30002, HTTPS: false, PublicPort: 80, Pool: "php-7-2-0", Extra: `C:\nido\config\sitios\viejo\nginx.conf`},
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
		"upstream nido_php {",
		"server 127.0.0.1:9100 max_fails=0;",
		"server 127.0.0.1:9101 max_fails=0;",
		"upstream nido_php_7_2_0 {",
		"server 127.0.0.1:9200 max_fails=0;",
		"listen 127.0.0.1:30000;",
		"listen 127.0.0.1:30001;",
		"server_name tienda.test *.tienda.test admin.tienda.test;",
		`root "E:/www/mi tienda/public";`,
		"try_files $uri $uri/ /index.php?$query_string;",
		"fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;",
		"fastcgi_param SERVER_PORT 443;",
		"fastcgi_param HTTPS on;",
		"fastcgi_param SERVER_PORT 80;",
		`include "C:/nido/bin/nginx/1.30.6/conf/fastcgi_params";`,
		`fastcgi_temp_path "` + paths.Slash(Prefix(home)) + `/temp/fastcgi";`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nginx.conf no contiene %q", want)
		}
	}
	// Reload finds the generation and the health-check port, and the health check answers with it.
	m := generationLine.FindStringSubmatch(text)
	if m == nil || m[2] != "30000" || !strings.Contains(text, "add_header X-Nido-Generacion "+m[1]+" always;") {
		t.Errorf("falta la generación de la configuración: %v", m)
	}
	// The plain-HTTP site must not claim HTTPS, and it goes to the PHP it pinned.
	tienda := text[strings.Index(text, "# tienda.test"):strings.Index(text, "# viejo.test")]
	viejo := text[strings.Index(text, "# viejo.test"):]
	if strings.Contains(viejo, "HTTPS on") {
		t.Error("viejo.test no usa HTTPS")
	}
	if !strings.Contains(tienda, "fastcgi_pass nido_php;") || !strings.Contains(viejo, "fastcgi_pass nido_php_7_2_0;") {
		t.Error("cada sitio debe ir a su grupo de PHP")
	}
	// A site's own directives come before Nido's locations, so their regular expressions win.
	include := strings.Index(viejo, `include "C:/nido/config/sitios/viejo/nginx.conf";`)
	if include < 0 || include > strings.Index(viejo, "location / {") {
		t.Error("viejo.test debe incluir sus directivas propias antes de sus location")
	}
	if strings.Contains(tienda, "sitios") {
		t.Error("tienda.test no tiene directivas propias")
	}
	for _, d := range []string{"temp/fastcgi", "temp/client_body", "logs"} {
		if _, err := os.Stat(Prefix(home) + "/" + d); err != nil {
			t.Errorf("falta la carpeta %s", d)
		}
	}
}
