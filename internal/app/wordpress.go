package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nicotc/laraclon/internal/install"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/versions"
)

// WordPress projects are installed with WP-CLI, which clon downloads with the first one and keeps
// a month before fetching it again.
const wpcliMaxAge = 30 * 24 * time.Hour

// localeRe accepts WordPress's locales: es_ES, en_US, pt_BR, de_DE_formal…
var localeRe = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?(_[a-z]+)?$`)

// createWordPress checks a WordPress request and starts installing it: MySQL, or MariaDB when there
// is no MySQL (WordPress runs on neither SQLite nor PostgreSQL).
func (d *Daemon) createWordPress(p NewProject, name, dir, cancelled string) (Job, error) {
	var kinds []versions.Component
	switch p.DB {
	case "":
		kinds = []versions.Component{versions.MySQL, versions.MariaDB}
	case "mysql":
		kinds = []versions.Component{versions.MySQL}
	case "mariadb":
		kinds = []versions.Component{versions.MariaDB}
	default:
		return Job{}, fmt.Errorf("WordPress funciona con MySQL o MariaDB, no con %s", p.DB)
	}
	var ref InstanceRef
	ok := false
	for _, k := range kinds {
		if ref, ok = d.env.DefaultInstance(k); ok {
			break
		}
	}
	if !ok {
		need := "MySQL o MariaDB"
		if len(kinds) == 1 {
			need = kinds[0].Name()
		}
		return Job{}, fmt.Errorf("WordPress necesita %s: instálalo en Servicios", need)
	}
	locale := strings.TrimSpace(p.Locale)
	if locale == "" {
		locale = "es_ES"
	}
	if !localeRe.MatchString(locale) {
		return Job{}, fmt.Errorf("idioma no válido %q: usa uno de WordPress, por ejemplo es_ES, es_MX o en_US", locale)
	}
	return d.startProjectJob(name, "WordPress", "nuevo-"+name, cancelled,
		func(ctx context.Context, phase func(string), log *os.File) (string, error) {
			return d.buildWordPress(ctx, p, name, dir, ref, locale, phase, log)
		})
}

// buildWordPress installs WordPress: its files and translation, the database, wp-config.php, the site
// in that language with an administrator (the note returned carries the generated password), the
// translations of its theme and plugins, and pretty permalinks.
func (d *Daemon) buildWordPress(ctx context.Context, p NewProject, name, dir string, ref InstanceRef, locale string, phase func(string), log *os.File) (string, error) {
	global, err := d.env.GlobalPHP()
	if err != nil {
		return "", err
	}
	ini, err := php.WriteIni(d.env.Home, global, d.env.IniOptions())
	if err != nil {
		return "", err
	}
	phar := install.WPCLIPhar(d.env.Home)
	if st, err := os.Stat(phar); err != nil || time.Since(st.ModTime()) > wpcliMaxAge {
		phase("descargando WP-CLI")
		if _, err := install.InstallWPCLI(ctx, d.env.Home); err != nil {
			if !fileExists(phar) {
				return "", err
			}
			fmt.Fprintf(log, "\r\nno se pudo actualizar WP-CLI (se usa el que había): %v\r\n", err)
		}
		d.refreshShims()
	}
	env := []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH()}
	// Run from the site's folder: WP-CLI reads its wp-cli.yml from there.
	wp := func(args ...string) error {
		return procutil.RunTree(ctx, dir, env, log, filepath.Join(global.Path, "php.exe"),
			append([]string{"-c", ini, phar, "--path=" + dir}, args...)...)
	}

	phase("descargando WordPress")
	version, err := install.InstallWordPress(ctx, d.env.Home, dir, nil)
	if err != nil {
		return "", err
	}
	phase("comprobando los archivos de WordPress " + version)
	if err := wp("core", "verify-checksums"); err != nil {
		return "", fmt.Errorf("wp core verify-checksums: %w", err)
	}
	// wordpress.org no longer packages every release in every language: the translation goes into
	// wp-content\languages before the installation, which then creates its first content in it.
	translated := locale == "en_US"
	if !translated {
		phase("descargando la traducción " + locale)
		if translated, err = install.InstallWordPressLanguage(ctx, d.env.Home, dir, version, locale); err != nil {
			return "", err
		}
	}
	dbName := strings.ReplaceAll(name, "-", "_")
	phase(fmt.Sprintf("creando la base de datos %s en %s %s", dbName, ref.Kind.Name(), ref.Version.Version))
	if d.sup.PID(ref.Key()) == 0 {
		if err := d.StartService(ref.Key()); err != nil {
			return "", err
		}
	}
	if err := d.createDatabase(ctx, ref, dbName, log); err != nil {
		return "", err
	}
	phase("escribiendo wp-config.php")
	if err := wp("config", "create", "--dbname="+dbName, "--dbuser=root", "--dbpass=",
		fmt.Sprintf("--dbhost=127.0.0.1:%d", ref.Instance.Port)); err != nil {
		return "", fmt.Errorf("wp config create: %w", err)
	}
	phase("instalando WordPress")
	password, err := generatePassword(16)
	if err != nil {
		return "", err
	}
	url := d.projectURL(name)
	args := []string{"core", "install", "--url=" + url, "--title=" + strings.TrimSpace(p.Name), "--admin_user=admin",
		"--admin_password=" + password, "--admin_email=admin@" + name + "." + d.env.Cfg.TLD, "--skip-email"}
	if translated && locale != "en_US" {
		// WP-CLI does not load the translation before WordPress creates its first post and page, as
		// the web installer does; the hook loads it right before (locale passed localeRe).
		args = append(args, "--locale="+locale,
			"--exec=WP_CLI::add_wp_hook('populate_options', function () { load_default_textdomain('"+locale+"'); });")
	}
	if err := wp(args...); err != nil {
		return "", fmt.Errorf("wp core install: %w", err)
	}
	note := fmt.Sprintf("Entra en %s/wp-admin con el usuario admin y la contraseña %s", url, password)
	switch {
	case !translated:
		note += fmt.Sprintf(" (WordPress no tiene traducción a %s: quedó en inglés)", locale)
	case locale != "en_US":
		// The theme's and plugins' own translations; without them the site still works.
		phase("traduciendo el tema y los plugins")
		for _, kind := range []string{"theme", "plugin"} {
			if err := wp("language", kind, "install", "--all", locale); err != nil {
				fmt.Fprintf(log, "\r\nwp language %s install --all %s: %v\r\n", kind, locale, err)
			}
		}
	}
	// WP-CLI writes the .htaccess of pretty permalinks once it knows Apache has mod_rewrite.
	phase("activando los enlaces permanentes")
	if err := os.WriteFile(filepath.Join(dir, "wp-cli.yml"), []byte("apache_modules:\n  - mod_rewrite\n"), 0o644); err != nil {
		return "", err
	}
	if err := wp("rewrite", "structure", "/%postname%/", "--hard"); err != nil {
		return "", fmt.Errorf("wp rewrite structure: %w", err)
	}
	return note, nil
}

// generatePassword makes a random password of letters and digits (easy to copy from the panel).
func generatePassword(n int) (string, error) {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, n)
	for i := range out {
		k, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		out[i] = chars[k.Int64()]
	}
	return string(out), nil
}
