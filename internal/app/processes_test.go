package app

import (
	"reflect"
	"testing"

	"github.com/nicotc/laraclon/internal/sites"
)

func TestSiteProcessesDependOnTheProject(t *testing.T) {
	cases := []struct {
		site sites.Site
		want []string
	}{
		{sites.Site{Kind: "laravel", Vite: true}, []string{"vite", "queue", "schedule"}},
		{sites.Site{Kind: "laravel"}, []string{"queue", "schedule"}},
		{sites.Site{Kind: "laravel", Vite: true, Reverb: true}, []string{"vite", "queue", "schedule", "reverb"}},
		{sites.Site{Kind: "php", Vite: true}, []string{"vite"}},
		{sites.Site{Kind: "wordpress"}, nil},
	}
	for _, c := range cases {
		if got := siteProcesses(c.site); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: got %v, want %v", c.site, got, c.want)
		}
	}
	if !isProcessService("vite-tienda") || !isProcessService("queue-mi-blog") || !isProcessService("reverb-tienda") || isProcessService("mysql-8.4") || isProcessService("php-cgi-9100") {
		t.Error("isProcessService must recognise only the project processes")
	}
}

func TestReverbIsPublishedAtItsOwnNameThroughTheProxy(t *testing.T) {
	s := sites.Site{Name: "tienda", Host: "tienda.test", HTTPS: true, Reverb: true, ReverbPort: 18001}
	r, ok := reverbRoute(s)
	if !ok || reverbHost(s) != "reverb.tienda.test" || r.Backend.String() != "http://127.0.0.1:18001" || !r.HTTPS {
		t.Fatalf("got %v %+v", ok, r)
	}
	d := &Daemon{env: &Env{}}
	d.env.Cfg.Proxy.HTTPSPort = 8444
	want := [][2]string{{"REVERB_HOST", "reverb.tienda.test"}, {"REVERB_PORT", "8444"}, {"REVERB_SCHEME", "https"},
		{"REVERB_SERVER_HOST", "127.0.0.1"}, {"REVERB_SERVER_PORT", "18001"}}
	if got := d.reverbEnv(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if _, ok := reverbRoute(sites.Site{Name: "blog", Host: "blog.test"}); ok {
		t.Fatal("a site without Reverb has no Reverb route")
	}
}
