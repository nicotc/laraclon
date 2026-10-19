package app

import (
	"path/filepath"
	"testing"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/sites"
)

func TestEachServerGivesItsSitesItsOwnPortsAndApacheKeepsTheirs(t *testing.T) {
	dir := t.TempDir()
	apacheStore, _ := sites.LoadPorts(filepath.Join(dir, "a.json"))
	nginxStore, _ := sites.LoadPorts(filepath.Join(dir, "n.json"))
	frankenStore, _ := sites.LoadPorts(filepath.Join(dir, "f.json"))
	others := map[string]serverPorts{
		config.ServerNginx:      {nginxStore, nginxPortFrom, nginxPortTo},
		config.ServerFrankenPHP: {frankenStore, frankenPortFrom, frankenPortTo},
	}
	list := func(tienda, api string) []sites.Site {
		return []sites.Site{{Name: "blog", Server: config.ServerApache}, {Name: "tienda", Server: tienda}, {Name: "api", Server: api}}
	}

	first := list(config.ServerApache, config.ServerApache)
	if err := assignPorts(first, apacheStore, others, nil); err != nil {
		t.Fatal(err)
	}
	apachePort := first[1].Port

	moved := list(config.ServerNginx, config.ServerFrankenPHP)
	own := map[string]map[int]bool{config.ServerApache: {first[0].Port: true}}
	if err := assignPorts(moved, apacheStore, others, own); err != nil {
		t.Fatal(err)
	}
	if p := moved[1].Port; p < nginxPortFrom || p > nginxPortTo {
		t.Fatalf("an Nginx site must use an Nginx port, got %d", p)
	}
	if p := moved[2].Port; p < frankenPortFrom || p > frankenPortTo {
		t.Fatalf("a FrankenPHP site must use a FrankenPHP port, got %d", p)
	}
	if moved[0].Port != first[0].Port {
		t.Fatal("the other sites keep their ports")
	}

	back := list(config.ServerApache, config.ServerApache)
	if err := assignPorts(back, apacheStore, others, own); err != nil {
		t.Fatal(err)
	}
	if back[1].Port != apachePort {
		t.Fatalf("switching back to Apache must reuse port %d, got %d", apachePort, back[1].Port)
	}
}
