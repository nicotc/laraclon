package app

import (
	"testing"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestSymfonyDatabaseURLPointsDoctrineAtNidosDatabases(t *testing.T) {
	ref := func(kind versions.Component, version string, port int) InstanceRef {
		return InstanceRef{Kind: kind, Version: versions.Version{Version: version}, Instance: config.ServiceInstance{Port: port}}
	}
	cases := []struct {
		ref  InstanceRef
		want string
	}{
		{ref(versions.MySQL, "8.4.11", 3306), "mysql://root:@127.0.0.1:3306/tienda?serverVersion=8.4.11&charset=utf8mb4"},
		{ref(versions.MariaDB, "11.8.9", 3310), "mysql://root:@127.0.0.1:3310/tienda?serverVersion=11.8.9-MariaDB&charset=utf8mb4"},
		{ref(versions.PostgreSQL, "18.6", 5432), "postgresql://postgres:@127.0.0.1:5432/tienda?serverVersion=18&charset=utf8"},
		{InstanceRef{}, "sqlite:///%kernel.project_dir%/var/data_%kernel.environment%.db"},
	}
	for _, c := range cases {
		user := "root"
		if c.ref.Kind == versions.PostgreSQL {
			user = "postgres"
		}
		if got := symfonyDatabaseURL(projectDB{ref: c.ref, name: "tienda", user: user}); got != c.want {
			t.Errorf("%s: got %s", c.ref.Kind, got)
		}
	}
	own := projectDB{ref: ref(versions.MySQL, "8.4.11", 3306), name: "tienda_db", user: "tienda_db", password: "aB3x", own: true}
	if got := symfonyDatabaseURL(own); got != "mysql://tienda_db:aB3x@127.0.0.1:3306/tienda_db?serverVersion=8.4.11&charset=utf8mb4" {
		t.Errorf("own user: got %s", got)
	}
}
