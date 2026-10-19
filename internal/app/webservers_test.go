package app

import (
	"path/filepath"
	"testing"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/sites"
)

func TestNginxSitesGetTheirOwnPortAndKeepTheApacheOne(t *testing.T) {
	dir := t.TempDir()
	apacheStore, _ := sites.LoadPorts(filepath.Join(dir, "a.json"))
	nginxStore, _ := sites.LoadPorts(filepath.Join(dir, "n.json"))
	list := func(server string) []sites.Site {
		return []sites.Site{{Name: "blog", Server: config.ServerApache}, {Name: "tienda", Server: server}}
	}

	first := list(config.ServerApache)
	if err := assignPorts(first, apacheStore, nginxStore, nil, nil); err != nil {
		t.Fatal(err)
	}
	apachePort := first[1].Port
	if apachePort < sitePortFrom || apachePort > sitePortTo {
		t.Fatalf("Apache port %d out of range", apachePort)
	}

	moved := list(config.ServerNginx)
	if err := assignPorts(moved, apacheStore, nginxStore, map[int]bool{first[0].Port: true}, nil); err != nil {
		t.Fatal(err)
	}
	if p := moved[1].Port; p < nginxPortFrom || p > nginxPortTo {
		t.Fatalf("an Nginx site must use an Nginx port, got %d", p)
	}
	if moved[0].Port != first[0].Port {
		t.Fatal("the other sites keep their ports")
	}

	back := list(config.ServerApache)
	if err := assignPorts(back, apacheStore, nginxStore, map[int]bool{first[0].Port: true}, nil); err != nil {
		t.Fatal(err)
	}
	if back[1].Port != apachePort {
		t.Fatalf("switching back to Apache must reuse port %d, got %d", apachePort, back[1].Port)
	}
}
