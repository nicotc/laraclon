package frankenphp

import (
	"os"
	"strings"
	"testing"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestWriteGeneratesOneSiteBlockPerSite(t *testing.T) {
	home := paths.Home{Root: t.TempDir()}
	conf, err := Write(Options{
		Home:      home,
		Version:   versions.Version{Component: versions.FrankenPHP, Version: "1.13.1", Path: `C:\clon\bin\frankenphp\1.13.1`, PHP: "8.5.11"},
		AdminPort: 40000,
		Sites: []Site{
			{Host: "rapida.test", DocRoot: `E:\www\mi rapida\public`, Port: 40001, HTTPS: true, PublicPort: 443},
			{Host: "plano.test", DocRoot: `E:\www\plano`, Port: 40002, PublicPort: 80},
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
		"admin 127.0.0.1:40000",
		"persist_config off",
		"auto_https off",
		"http://:40001 {",
		"bind 127.0.0.1",
		`root * "E:/www/mi rapida/public"`,
		"env HTTPS on",
		"env SERVER_PORT 443",
		"env SERVER_NAME rapida.test",
		"http://:40002 {",
		"env SERVER_PORT 80",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("el Caddyfile no contiene %q", want)
		}
	}
	plano := text[strings.Index(text, "# plano.test"):]
	if strings.Contains(plano, "HTTPS on") {
		t.Error("plano.test no usa HTTPS")
	}
}
