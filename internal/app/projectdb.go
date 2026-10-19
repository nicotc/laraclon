package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nicotc/laraclon/internal/mysql"
	"github.com/nicotc/laraclon/internal/postgres"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/versions"
)

// dbNameRe is what a database name may hold: the same for MySQL, MariaDB and PostgreSQL, without
// quoting surprises.
var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,63}$`)

// projectDB is where a new project keeps its data and how its settings reach it.
type projectDB struct {
	ref      InstanceRef // the server (none for SQLite)
	name     string      // the database
	user     string      // root, postgres or a user of the project's own
	password string
	own      bool // the user was created for this project
}

// pickInstance chooses the server of a new project's database: the one the request names ("mysql-8.0")
// or the default of the first of kinds that has one.
func (d *Daemon) pickInstance(kinds []versions.Component, key string) (InstanceRef, error) {
	if key = strings.ToLower(strings.TrimSpace(key)); key != "" {
		for _, k := range kinds {
			for _, ref := range d.env.Instances(k) {
				if ref.Key() == key {
					return ref, nil
				}
			}
		}
		return InstanceRef{}, fmt.Errorf("no hay ninguna base de datos %s instalada (mira 'nido service list')", key)
	}
	for _, k := range kinds {
		if ref, ok := d.env.DefaultInstance(k); ok {
			return ref, nil
		}
	}
	names := []string{}
	for _, k := range kinds {
		names = append(names, k.Name())
	}
	return InstanceRef{}, fmt.Errorf("%s no está instalado: instálalo en Servicios o elige otra base de datos", strings.Join(names, " o "))
}

// planProjectDB checks a request's database settings: the server, the name (the project's with
// underscores when none is given) and whether it gets a user of its own.
func (d *Daemon) planProjectDB(p NewProject, kinds []versions.Component, project string) (projectDB, error) {
	ref, err := d.pickInstance(kinds, p.DBKey)
	if err != nil {
		return projectDB{}, err
	}
	name := strings.TrimSpace(p.DBName)
	if name == "" {
		name = strings.ReplaceAll(project, "-", "_")
	}
	if !dbNameRe.MatchString(name) {
		return projectDB{}, fmt.Errorf("«%s» no sirve como nombre de base de datos: usa letras, números y _ (hasta 63)", name)
	}
	db := projectDB{ref: ref, name: name, user: "root"}
	if ref.Kind == versions.PostgreSQL {
		db.user = "postgres"
	}
	if p.DBUser {
		pw, err := generatePassword(20)
		if err != nil {
			return projectDB{}, err
		}
		db.user, db.password, db.own = strings.ToLower(name), pw, true
		if len(db.user) > 32 { // MySQL's limit
			db.user = db.user[:32]
		}
	}
	return db, nil
}

// create starts the server when needed and creates the database, and the project's user with
// rights on it alone. An existing database is kept as it is.
func (db projectDB) create(ctx context.Context, d *Daemon, log *os.File) error {
	if d.sup.PID(db.ref.Key()) == 0 {
		if err := d.StartService(db.ref.Key()); err != nil {
			return err
		}
	}
	if err := d.createDatabase(ctx, db.ref, db.name, log); err != nil {
		return err
	}
	if !db.own {
		return nil
	}
	port := fmt.Sprint(db.ref.Instance.Port)
	if db.ref.Kind == versions.PostgreSQL {
		psql := postgres.Psql(db.ref.Version)
		sql := fmt.Sprintf(`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN CREATE ROLE "%[1]s" LOGIN PASSWORD '%[2]s'; `+
			`ELSE ALTER ROLE "%[1]s" WITH LOGIN PASSWORD '%[2]s'; END IF; END $$; ALTER DATABASE "%[3]s" OWNER TO "%[1]s";`, db.user, db.password, db.name)
		return procutil.RunTree(ctx, filepath.Dir(psql), nil, log, psql, "-h", "127.0.0.1", "-p", port, "-U", "postgres", "-c", sql)
	}
	client := mysql.ClientExe(db.ref.Version)
	if client == "" {
		return fmt.Errorf("%s %s no trae el cliente mysql", db.ref.Kind.Name(), db.ref.Version.Version)
	}
	// Both names a local connection can arrive with, by name or by address.
	var sql strings.Builder
	for _, host := range []string{"localhost", "127.0.0.1"} {
		fmt.Fprintf(&sql, "CREATE USER IF NOT EXISTS '%[1]s'@'%[2]s' IDENTIFIED BY '%[3]s'; ALTER USER '%[1]s'@'%[2]s' IDENTIFIED BY '%[3]s'; "+
			"GRANT ALL PRIVILEGES ON `%[4]s`.* TO '%[1]s'@'%[2]s'; ", db.user, host, db.password, db.name)
	}
	sql.WriteString("FLUSH PRIVILEGES;")
	return procutil.RunTree(ctx, filepath.Dir(client), nil, log, client, "--host=127.0.0.1", "--port="+port, "--user=root", "-e", sql.String())
}

// note tells the project's own user and password at the end of the job.
func (db projectDB) note() string {
	if !db.own {
		return ""
	}
	return fmt.Sprintf("Base de datos %s con su propio usuario: %s, contraseña %s (está en la configuración del proyecto).", db.name, db.user, db.password)
}
