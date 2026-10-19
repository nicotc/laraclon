package app

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestDebuggedSitesGetAPoolWithXdebug(t *testing.T) {
	reg := &versions.Registry{}
	for _, v := range []string{"8.4.16", "7.2.0"} {
		reg.Add(versions.Version{Component: versions.PHP, Version: v})
	}
	global, _ := reg.Find(versions.PHP, "8.4")
	store, _ := loadPoolStore(filepath.Join(t.TempDir(), "php-pools.json"))
	list := []sites.Site{
		{Name: "app", Server: config.ServerApache},
		{Name: "depurar", Server: config.ServerApache, Debug: true},           // global version, with Xdebug
		{Name: "otro", Server: config.ServerNginx, Debug: true},               // shares that pool
		{Name: "viejo", Server: config.ServerApache, PHP: "7.2", Debug: true}, // pinned and debugged
	}
	pools, notes := planPools(list, reg, global, []int{9100}, store, 49300, 49399, nil)
	if len(notes) != 0 {
		t.Fatalf("notes: %v", notes)
	}
	got := map[string]string{}
	for _, s := range list {
		got[s.Name] = s.Pool
	}
	want := map[string]string{"app": "php", "depurar": "php-8-4-16-debug", "otro": "php-8-4-16-debug", "viejo": "php-7-2-0-debug"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(pools) != 3 || !pools[1].Debug || pools[1].Sites != 2 || pools[1].Version.Version != "8.4.16" || !pools[2].Debug {
		t.Fatalf("pools %+v", pools)
	}
	// The debug pools keep their ports, apart from the plain pool of the same version.
	if store.Ports["8.4.16-debug"] == 0 || store.Ports["7.2.0-debug"] == 0 {
		t.Fatalf("store %v", store.Ports)
	}
}

func TestPinnedSitesGetAPoolOfTheirVersion(t *testing.T) {
	reg := &versions.Registry{}
	for _, v := range []string{"8.4.16", "8.3.12", "7.2.0"} {
		reg.Add(versions.Version{Component: versions.PHP, Version: v})
	}
	global, _ := reg.Find(versions.PHP, "8.4")
	store, err := loadPoolStore(filepath.Join(t.TempDir(), "php-pools.json"))
	if err != nil {
		t.Fatal(err)
	}
	list := []sites.Site{
		{Name: "app", Host: "app.test", Server: config.ServerApache},
		{Name: "viejo", Host: "viejo.test", Server: config.ServerApache, PHP: "7.2"},
		{Name: "igual", Host: "igual.test", Server: config.ServerNginx, PHP: "8.4"},
		{Name: "falta", Host: "falta.test", Server: config.ServerApache, PHP: "5.6"},
		{Name: "franken", Host: "franken.test", Server: config.ServerFrankenPHP, PHP: "7.2"},
		{Name: "otro", Host: "otro.test", Server: config.ServerNginx, PHP: "7.2.0"},
	}
	const from, to = 49200, 49299 // high ports, free on a test machine
	pools, notes := planPools(list, reg, global, []int{9100, 9101}, store, from, to, nil)

	if len(pools) != 2 || !pools[0].Global || pools[0].Sites != 3 {
		t.Fatalf("want the global pool (app, igual, falta) and one for 7.2, got %+v", pools)
	}
	pinned := pools[1]
	if pinned.Name != "php-7-2-0" || pinned.Version.Version != "7.2.0" || pinned.Sites != 2 || len(pinned.Ports) != pinnedPoolSize {
		t.Fatalf("pinned pool %+v", pinned)
	}
	if pinned.Ports[0] < from || pinned.Ports[len(pinned.Ports)-1] > to {
		t.Fatalf("pinned ports %v outside %d-%d", pinned.Ports, from, to)
	}
	want := map[string][2]string{
		"app": {"php", "8.4.16"}, "viejo": {"php-7-2-0", "7.2.0"}, "igual": {"php", "8.4.16"},
		"falta": {"php", "8.4.16"}, "franken": {"", ""}, "otro": {"php-7-2-0", "7.2.0"},
	}
	for _, s := range list {
		if got := [2]string{s.Pool, s.PHPVersion}; got != want[s.Name] {
			t.Errorf("%s: pool %q PHP %q, want %v", s.Name, s.Pool, s.PHPVersion, want[s.Name])
		}
	}
	if len(notes) != 1 {
		t.Fatalf("one note for the missing PHP 5.6, got %v", notes)
	}

	// The pinned version keeps its ports when planned again.
	again, _ := planPools(list, reg, global, []int{9100, 9101}, store, from, to, nil)
	if !reflect.DeepEqual(again[1].Ports, pinned.Ports) {
		t.Fatalf("ports moved from %v to %v", pinned.Ports, again[1].Ports)
	}
	// When the global PHP becomes the pinned version, its sites go back to the global pool, and the
	// site that pinned the former global version gets a pool of its own.
	old, _ := reg.Find(versions.PHP, "7.2")
	back, _ := planPools(list, reg, old, []int{9100, 9101}, store, from, to, nil)
	if len(back) != 2 || back[0].Sites != 4 || back[1].Version.Version != "8.4.16" || back[1].Sites != 1 {
		t.Fatalf("want the global pool (app, viejo, falta, otro) and one for 8.4 (igual), got %+v", back)
	}
	if reflect.DeepEqual(back[1].Ports, pinned.Ports) {
		t.Fatalf("8.4 must not take the ports kept for 7.2 (%v)", pinned.Ports)
	}
}
