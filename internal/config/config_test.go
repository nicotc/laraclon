package config

import (
	"reflect"
	"testing"
)

func TestSetExtensionWritesOnlyRealChanges(t *testing.T) {
	var p PHP
	p.SetExtension("8.4", "xdebug", true, false) // not a default: recorded as enabled
	p.SetExtension("8.4", "exif", false, true)   // a default: recorded as disabled
	if want := (PHPExtensions{Enable: []string{"xdebug"}, Disable: []string{"exif"}}); !reflect.DeepEqual(p.Extensions["8.4"], want) {
		t.Fatalf("got %+v, want %+v", p.Extensions["8.4"], want)
	}
	p.SetExtension("8.4", "xdebug", false, false) // back to clon's default
	p.SetExtension("8.4", "exif", true, true)
	if _, ok := p.Extensions["8.4"]; ok {
		t.Fatalf("nothing differs from the defaults any more, got %+v", p.Extensions)
	}
}

func TestSiteSettingsAreDroppedWhenEmpty(t *testing.T) {
	var c Config
	c.SetSitePHP("tienda", "7.2")
	c.SetSiteServer("tienda", ServerNginx)
	if s := c.Sites["tienda"]; s.PHP != "7.2" || s.Server != ServerNginx {
		t.Fatalf("got %+v", s)
	}
	c.SetSitePHP("tienda", "")
	c.SetSiteServer("tienda", ServerApache)
	if _, ok := c.Sites["tienda"]; ok {
		t.Fatal("a site back to its defaults leaves no section in clon.toml")
	}
}
