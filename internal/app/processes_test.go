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
		{sites.Site{Kind: "php", Vite: true}, []string{"vite"}},
		{sites.Site{Kind: "wordpress"}, nil},
	}
	for _, c := range cases {
		if got := siteProcesses(c.site); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: got %v, want %v", c.site, got, c.want)
		}
	}
	if !isProcessService("vite-tienda") || !isProcessService("queue-mi-blog") || isProcessService("mysql-8.4") || isProcessService("php-cgi-9100") {
		t.Error("isProcessService must recognise only the project processes")
	}
}
