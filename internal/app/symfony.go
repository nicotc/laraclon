package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/versions"
)

// createSymfony checks a Symfony request and starts creating it: symfony/skeleton, plus the webapp
// pack (Twig, Doctrine, forms, security…) when asked, wired to one of clon's databases.
func (d *Daemon) createSymfony(p NewProject, name, root, dir, cancelled string) (Job, error) {
	if _, err := os.Stat(install.ComposerPhar(d.env.Home)); err != nil {
		return Job{}, fmt.Errorf("falta Composer: instálalo con 'clon composer install'")
	}
	var ref InstanceRef
	if p.Webapp {
		if p.DB == "" {
			p.DB = "sqlite"
		}
		db, ok := projectDBs[p.DB]
		if !ok {
			return Job{}, fmt.Errorf("base de datos desconocida %q: usa sqlite, mysql, mariadb o postgresql", p.DB)
		}
		if db.kind != "" {
			if ref, ok = d.env.DefaultInstance(db.kind); !ok {
				return Job{}, fmt.Errorf("%s no está instalado: instálalo en Servicios o elige otra base de datos", db.kind.Name())
			}
		}
	}
	label := "Symfony"
	if p.Webapp {
		label = "Symfony webapp"
	}
	return d.startCreateJob(p, name, dir, label, cancelled,
		func(ctx context.Context, phase func(string), log *os.File) (string, error) {
			return "", d.buildSymfony(ctx, p, name, root, dir, ref, phase, log)
		})
}

// buildSymfony creates a Symfony project with Composer. Apache gets the public\.htaccess of
// symfony/apache-pack (a "contrib" recipe, which Flex only runs when the project allows them), and
// .env.local points the project at https://<name>.test and, with webapp, at clon's database and
// Mailpit.
func (d *Daemon) buildSymfony(ctx context.Context, p NewProject, name, root, dir string, ref InstanceRef, phase func(string), log *os.File) error {
	global, err := d.env.GlobalPHP()
	if err != nil {
		return err
	}
	ini, err := php.WriteIni(d.env.Home, global, d.env.IniOptions())
	if err != nil {
		return err
	}
	env := []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH(), "COMPOSER_NO_INTERACTION=1"}
	composer := func(in string, args ...string) error {
		return procutil.RunTree(ctx, in, env, log, filepath.Join(global.Path, "php.exe"),
			append([]string{"-c", ini, install.ComposerPhar(d.env.Home)}, args...)...)
	}

	// Composer picks the newest Symfony the global PHP can run (8.x needs PHP 8.4, 7.4 LTS PHP 8.2).
	phase("descargando Symfony con Composer")
	if err := composer(root, "create-project", "symfony/skeleton", dir, "--prefer-dist", "--no-interaction"); err != nil {
		return fmt.Errorf("composer create-project: %w", err)
	}
	// Recipes from symfony/recipes-contrib run without asking, and no Docker files are added.
	if err := composer(dir, "config", "--json", "extra.symfony.allow-contrib", "true"); err != nil {
		return fmt.Errorf("composer config: %w", err)
	}
	if err := composer(dir, "config", "--json", "extra.symfony.docker", "false"); err != nil {
		return fmt.Errorf("composer config: %w", err)
	}
	phase("añadiendo el .htaccess para Apache (symfony/apache-pack)")
	if err := composer(dir, "require", "symfony/apache-pack", "--no-interaction"); err != nil {
		return fmt.Errorf("composer require symfony/apache-pack: %w", err)
	}
	if p.Webapp {
		phase("instalando webapp (Twig, Doctrine, formularios, seguridad…)")
		if err := composer(dir, "require", "webapp", "--no-interaction"); err != nil {
			return fmt.Errorf("composer require webapp: %w", err)
		}
	}

	phase("configurando .env.local")
	values := [][2]string{{"DEFAULT_URI", d.projectURL(name)}}
	if p.Webapp {
		dbName := strings.ReplaceAll(name, "-", "_")
		values = append(values, [2]string{"DATABASE_URL", symfonyDatabaseURL(ref, dbName)})
		if mail, ok := d.env.DefaultInstance(versions.Mailpit); ok {
			values = append(values, [2]string{"MAILER_DSN", fmt.Sprintf("smtp://127.0.0.1:%d", mail.Instance.Port)})
		}
		if ref.Kind != "" {
			phase(fmt.Sprintf("creando la base de datos %s en %s %s", dbName, ref.Kind.Name(), ref.Version.Version))
			if d.sup.PID(ref.Key()) == 0 {
				if err := d.StartService(ref.Key()); err != nil {
					return err
				}
			}
			if err := d.createDatabase(ctx, ref, dbName, log); err != nil {
				return err
			}
		}
	}
	envLocal := filepath.Join(dir, ".env.local")
	if !fileExists(envLocal) {
		if err := os.WriteFile(envLocal, []byte("# clon: valores de este equipo (no van a Git)\n"), 0o644); err != nil {
			return err
		}
	}
	return editEnvFile(envLocal, values)
}

// symfonyDatabaseURL is Doctrine's DATABASE_URL for one of clon's databases ("" kind: SQLite, the
// file Symfony's own .env suggests).
func symfonyDatabaseURL(ref InstanceRef, dbName string) string {
	switch ref.Kind {
	case versions.MySQL:
		return fmt.Sprintf("mysql://root:@127.0.0.1:%d/%s?serverVersion=%s&charset=utf8mb4", ref.Instance.Port, dbName, ref.Version.Version)
	case versions.MariaDB:
		return fmt.Sprintf("mysql://root:@127.0.0.1:%d/%s?serverVersion=%s-MariaDB&charset=utf8mb4", ref.Instance.Port, dbName, ref.Version.Version)
	case versions.PostgreSQL:
		return fmt.Sprintf("postgresql://postgres:@127.0.0.1:%d/%s?serverVersion=%s&charset=utf8", ref.Instance.Port, dbName, versions.Series(versions.PostgreSQL, ref.Version.Version))
	}
	return "sqlite:///%kernel.project_dir%/var/data_%kernel.environment%.db"
}
