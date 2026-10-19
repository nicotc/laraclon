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
		Version:   versions.Version{Component: versions.Nginx, Version: "1.30.6", Path: `C:\clon\bin\nginx\1.30.6`},
		AdminPort: 30000,
		PHPPorts:  []int{9100, 9101},
		Sites: []Site{
			{Host: "tienda.test", Aliases: []string{"admin.tienda.test"}, DocRoot: `E:\www\mi tienda\public`, Port: 30001, HTTPS: true, PublicPort: 443},
			{Host: "viejo.test", DocRoot: `E:\www\viejo`, Port: 30002, HTTPS: false, PublicPort: 80},
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
		"server 127.0.0.1:9100 max_fails=0;",
		"server 127.0.0.1:9101 max_fails=0;",
		"listen 127.0.0.1:30000;",
		"listen 127.0.0.1:30001;",
		"server_name tienda.test *.tienda.test admin.tienda.test;",
		`root "E:/www/mi tienda/public";`,
		"try_files $uri $uri/ /index.php?$query_string;",
		"fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;",
		"fastcgi_param SERVER_PORT 443;",
		"fastcgi_param HTTPS on;",
		"fastcgi_param SERVER_PORT 80;",
		`include "C:/clon/bin/nginx/1.30.6/conf/fastcgi_params";`,
		`fastcgi_temp_path "` + paths.Slash(Prefix(home)) + `/temp/fastcgi";`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("nginx.conf no contiene %q", want)
		}
	}
	// The plain-HTTP site must not claim HTTPS.
	viejo := text[strings.Index(text, "# viejo.test"):]
	if strings.Contains(viejo, "HTTPS on") {
		t.Error("viejo.test no usa HTTPS")
	}
	for _, d := range []string{"temp/fastcgi", "temp/client_body", "logs"} {
		if _, err := os.Stat(Prefix(home) + "/" + d); err != nil {
			t.Errorf("falta la carpeta %s", d)
		}
	}
}
