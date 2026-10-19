package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nicotc/laraclon/internal/apache"
	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/mysql"
	"github.com/nicotc/laraclon/internal/nginx"
	"github.com/nicotc/laraclon/internal/php"
	"github.com/nicotc/laraclon/internal/procutil"
	"github.com/nicotc/laraclon/internal/proxy"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/versions"
)

// The configuration files the panel edits: each site's .env, .htaccess, .user.ini and its own
// Apache or Nginx directives, the php.ini additions of each PHP series and the my.ini additions of
// each MySQL or MariaDB instance. Saving keeps the previous version, checks the new one with the
// program that reads it and puts the previous one back when that program rejects it.

// EditableFile is one configuration file as the panel shows it.
type EditableFile struct {
	Key      string `json:"key"`   // "site:tienda:env", "php:8.4", "service:mysql-8.0"
	Title    string `json:"title"` // ".env", "public/.htaccess"
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Help     string `json:"help"`                // what it is for and how Nido applies it
	Note     string `json:"note,omitempty"`      // why it does not count right now
	Syntax   string `json:"syntax"`              // env, ini, apache or nginx
	Backups  int    `json:"backups"`             // earlier versions «Deshacer» brings back
	ReadOnly string `json:"read_only,omitempty"` // why the panel must not change it
	Content  string `json:"content,omitempty"`   // only when one file is read
	Hash     string `json:"hash,omitempty"`      // of the bytes on disk ("" while it does not exist)
	Starter  bool   `json:"starter,omitempty"`   // Content is a starting point: the file does not exist yet
}

// FileResult tells how a save or an undo went.
type FileResult struct {
	File     EditableFile `json:"file"`
	Message  string       `json:"message"`
	Warnings []string     `json:"warnings,omitempty"`
}

// fileSpec is an editable file plus how Nido checks and applies it.
type fileSpec struct {
	EditableFile
	starter string // offered while the file does not exist
	// check looks at the new content before it is written; an error leaves the file as it was.
	check func(content []byte) (warnings []string, err error)
	// apply makes the written file count (a reload, a restart, a test request); an error puts the
	// previous file back.
	apply func() (msg string, warnings []string, err error)
}

const (
	maxEditable = 1 << 20 // bigger files are not edited in the panel
	keepBackups = 10      // earlier versions kept per file
)

var errFileChanged = errors.New("el archivo cambió fuera de Nido desde que lo abriste: vuelve a abrirlo para no perder esos cambios")

// Files lists the editable files of a site ("site:tienda"), of PHP ("php") or of a database
// instance ("service:mysql-8.0").
func (d *Daemon) Files(scope string) ([]EditableFile, error) {
	specs, err := d.fileSpecs(scope)
	if err != nil {
		return nil, err
	}
	out := make([]EditableFile, 0, len(specs))
	for _, s := range specs {
		out = append(out, d.describe(s))
	}
	return out, nil
}

func (d *Daemon) fileSpecs(scope string) ([]fileSpec, error) {
	kind, rest, _ := strings.Cut(scope, ":")
	switch kind {
	case "site":
		s, err := d.findSite(rest)
		if err != nil {
			return nil, err
		}
		return d.siteFiles(s), nil
	case "php":
		return d.phpFiles(), nil
	case "service":
		spec, err := d.serviceFile(rest)
		if err != nil {
			return nil, err
		}
		return []fileSpec{spec}, nil
	}
	return nil, fmt.Errorf("Nido no tiene archivos editables de %q", scope)
}

// scopeOf is the scope of a file key: "site:tienda:env" → "site:tienda", "php:8.4" → "php".
func scopeOf(key string) string {
	switch {
	case strings.HasPrefix(key, "site:"):
		if i := strings.LastIndex(key, ":"); i > len("site:") {
			return key[:i]
		}
	case strings.HasPrefix(key, "php:"):
		return "php"
	}
	return key
}

func (d *Daemon) fileSpec(key string) (fileSpec, error) {
	specs, err := d.fileSpecs(scopeOf(key))
	if err != nil {
		return fileSpec{}, err
	}
	for _, s := range specs {
		if s.Key == key {
			return s, nil
		}
	}
	return fileSpec{}, fmt.Errorf("no hay ningún archivo editable %q", key)
}

// describe adds what the disk says about a file now.
func (d *Daemon) describe(s fileSpec) EditableFile {
	f := s.EditableFile
	data, err := os.ReadFile(f.Path)
	f.Exists = err == nil
	switch {
	case f.Exists && len(data) > maxEditable:
		f.ReadOnly = "es demasiado grande para editarlo aquí (más de 1 MB)"
	case f.Exists && !utf8.Valid(data):
		f.ReadOnly = "no está en UTF-8: Nido no lo edita para no estropear sus caracteres"
	}
	f.Backups = len(d.backups(f.Key))
	return f
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ReadEditable returns a file with its content, or with a starting content while it does not exist.
func (d *Daemon) ReadEditable(key string) (EditableFile, error) {
	s, err := d.fileSpec(key)
	if err != nil {
		return EditableFile{}, err
	}
	f := d.describe(s)
	data, existed, err := readIfExists(f.Path)
	switch {
	case err != nil:
		return f, err
	case existed:
		f.Hash = hashOf(data)
		f.Content = strings.ReplaceAll(string(bytes.TrimPrefix(data, utf8BOM)), "\r\n", "\n")
	default:
		f.Content, f.Starter = s.starter, true
	}
	return f, nil
}

// SaveFile writes a file edited in the panel. hash is the one ReadEditable gave: when the file
// changed on disk since then, nothing is written. The new content is checked before and applied
// after; when either fails, the file stays as it was (or goes back to it).
func (d *Daemon) SaveFile(key, content, hash string) (FileResult, error) {
	d.filesMu.Lock()
	defer d.filesMu.Unlock()
	s, err := d.fileSpec(key)
	if err != nil {
		return FileResult{}, err
	}
	if f := d.describe(s); f.ReadOnly != "" {
		return FileResult{}, fmt.Errorf("%s %s", f.Title, f.ReadOnly)
	}
	old, existed, err := readIfExists(s.Path)
	if err != nil {
		return FileResult{}, err
	}
	current := ""
	if existed {
		current = hashOf(old)
	}
	if current != hash {
		return FileResult{}, errFileChanged
	}
	data := fileBytes(content, old)
	if len(data) > maxEditable {
		return FileResult{}, fmt.Errorf("%s quedaría demasiado grande (más de 1 MB)", s.Title)
	}
	if existed && bytes.Equal(data, old) {
		return FileResult{File: d.describe(s), Message: "No había cambios."}, nil
	}
	var warnings []string
	if s.check != nil {
		if warnings, err = s.check(data); err != nil {
			return FileResult{}, err
		}
	}
	if err := d.keepBackup(key, old, existed); err != nil {
		return FileResult{}, err
	}
	if err := writeEditable(s.Path, data); err != nil {
		d.dropNewestBackup(key)
		return FileResult{}, err
	}
	msg, more, err := d.applyOrRevert(s, old, existed)
	if err != nil {
		d.dropNewestBackup(key) // a save that did not happen leaves no version behind
		return FileResult{}, err
	}
	return FileResult{File: d.describe(s), Message: msg, Warnings: append(warnings, more...)}, nil
}

// UndoFile brings back the version a file had before its last save, and applies it.
func (d *Daemon) UndoFile(key string) (FileResult, error) {
	d.filesMu.Lock()
	defer d.filesMu.Unlock()
	s, err := d.fileSpec(key)
	if err != nil {
		return FileResult{}, err
	}
	list := d.backups(key)
	if len(list) == 0 {
		return FileResult{}, fmt.Errorf("%s no tiene versiones anteriores", s.Title)
	}
	last := list[len(list)-1]
	prev, err := os.ReadFile(last)
	if err != nil {
		return FileResult{}, err
	}
	cur, existed, err := readIfExists(s.Path)
	if err != nil {
		return FileResult{}, err
	}
	if strings.HasSuffix(last, ".none") {
		err = removeIfExists(s.Path) // before that save, the file did not exist
	} else {
		err = writeEditable(s.Path, prev)
	}
	if err != nil {
		return FileResult{}, err
	}
	msg, warnings, err := d.applyOrRevert(s, cur, existed)
	if err != nil {
		return FileResult{}, err
	}
	os.Remove(last)
	return FileResult{File: d.describe(s), Message: "Volvió la versión anterior. " + msg, Warnings: warnings}, nil
}

// applyOrRevert applies a file just written. When the program that reads it says no, the previous
// file comes back (or goes away, when there was none) and is applied again.
func (d *Daemon) applyOrRevert(s fileSpec, old []byte, existed bool) (string, []string, error) {
	if s.apply == nil {
		return "Guardado.", nil, nil
	}
	msg, warnings, err := s.apply()
	if err == nil {
		return msg, warnings, nil
	}
	if existed {
		writeEditable(s.Path, old)
	} else {
		os.Remove(s.Path)
	}
	if _, _, again := s.apply(); again != nil {
		return "", nil, fmt.Errorf("%w\n\nNido volvió al archivo anterior, pero tampoco se aplicó: %v", err, again)
	}
	return "", nil, fmt.Errorf("%w\n\nNido dejó el archivo como estaba.", err)
}

// ---- versions kept ----

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// backupDir keeps the earlier versions of a file: <time>.bak with its content, or <time>.none
// when it did not exist yet.
func (d *Daemon) backupDir(key string) string {
	return d.env.Home.State("copias", unsafeName.ReplaceAllString(key, "_"))
}

// backups lists the earlier versions of a file, oldest first.
func (d *Daemon) backups(key string) []string {
	dir := d.backupDir(key)
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".bak") || strings.HasSuffix(n, ".none") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out) // the names start with the time
	return out
}

func (d *Daemon) keepBackup(key string, old []byte, existed bool) error {
	dir := d.backupDir(key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The name is the time, made unique when the clock has not moved since the last one.
	stamp := time.Now()
	name := func() string { return stamp.Format("20060102-150405.000000000") }
	for fileExists(filepath.Join(dir, name()+".bak")) || fileExists(filepath.Join(dir, name()+".none")) {
		stamp = stamp.Add(time.Microsecond)
	}
	file := name() + ".bak"
	if !existed {
		file, old = name()+".none", nil
	}
	if err := os.WriteFile(filepath.Join(dir, file), old, 0o644); err != nil {
		return err
	}
	for list := d.backups(key); len(list) > keepBackups; list = list[1:] {
		os.Remove(list[0])
	}
	return nil
}

func (d *Daemon) dropNewestBackup(key string) {
	if list := d.backups(key); len(list) > 0 {
		os.Remove(list[len(list)-1])
	}
}

// ---- reading and writing ----

func readIfExists(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return data, true, nil
	case errors.Is(err, os.ErrNotExist):
		return nil, false, nil
	}
	return nil, false, err
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fileBytes turns the editor's text into the file's bytes: the line endings the file already had
// (the editor works with \n), a line break at the end and no BOM.
func fileBytes(content string, old []byte) []byte {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if bytes.Contains(old, []byte("\r\n")) {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return []byte(text)
}

func writeEditable(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return config.WriteFileAtomic(path, data)
}

// ---- the files of a site ----

func siteKey(s sites.Site, id string) string { return "site:" + s.Name + ":" + id }

var serverNames = map[string]string{config.ServerApache: "Apache", config.ServerNginx: "Nginx", config.ServerFrankenPHP: "FrankenPHP"}

// directivesPath is where a site's own directives for a web server live.
func (d *Daemon) directivesPath(site, server string) string {
	return d.env.Home.Config("sitios", site, server+".conf")
}

// siteDirectives is the file with a site's own directives for a web server, "" while it has none.
func (d *Daemon) siteDirectives(site, server string) string {
	if p := d.directivesPath(site, server); fileExists(p) {
		return p
	}
	return ""
}

const htaccessStarter = `# Reglas de Apache para esta carpeta. Por ejemplo, llevar a index.php lo que no sea un archivo:
#
# <IfModule mod_rewrite.c>
#     RewriteEngine On
#     RewriteCond %{REQUEST_FILENAME} !-f
#     RewriteCond %{REQUEST_FILENAME} !-d
#     RewriteRule ^ index.php [L]
# </IfModule>
`

const userIniStarter = `; Valores de PHP solo para este sitio. Quita el ; de los que quieras cambiar:
; memory_limit = 1G
; upload_max_filesize = 128M
; post_max_size = 128M
; max_execution_time = 600
; display_errors = On
`

var directivesHelp = map[string]string{
	config.ServerApache: "Directivas de Apache solo para este sitio: van dentro de su <VirtualHost>, después de las de Nido. Apache las comprueba (httpd -t) y las aplica sin cortar las peticiones.",
	config.ServerNginx:  "Directivas de Nginx solo para este sitio: van dentro de su bloque server, antes de las de Nido. Nginx las comprueba (nginx -t) y las aplica sin cortar las peticiones.",
}

var directivesStarter = map[string]string{
	config.ServerApache: `# Directivas de Apache para %s. Por ejemplo:
#
# Header set X-Frame-Options "SAMEORIGIN"
# Alias /archivos "C:/ruta/a/la/carpeta"
# <Directory "C:/ruta/a/la/carpeta">
#     Require all granted
# </Directory>
`,
	config.ServerNginx: `# Directivas de Nginx para %s. Por ejemplo:
#
# client_max_body_size 2g;
# add_header X-Frame-Options "SAMEORIGIN";
# location /archivos/ {
#     alias "C:/ruta/a/la/carpeta/";
# }
`,
}

// siteFiles are the files of a site the panel edits.
func (d *Daemon) siteFiles(s sites.Site) []fileSpec {
	rel := func(p string) string {
		if r, err := filepath.Rel(s.Folder, p); err == nil {
			return filepath.ToSlash(r)
		}
		return p
	}
	server := serverNames[s.Server]
	var out []fileSpec

	env := filepath.Join(s.Folder, ".env")
	if s.Kind == "laravel" || s.Kind == "symfony" || fileExists(env) || fileExists(env+".example") {
		example, _ := os.ReadFile(env + ".example")
		out = append(out, fileSpec{
			EditableFile: EditableFile{Key: siteKey(s, "env"), Title: ".env", Path: env, Syntax: "env",
				Help: "Las variables del proyecto: base de datos, correo, claves, depuración. Nido revisa la sintaxis antes de guardar; en Laravel borra la caché de configuración si la hay, y reinicia los procesos del sitio para que lean el cambio."},
			starter: strings.ReplaceAll(string(example), "\r\n", "\n"),
			check:   func(b []byte) ([]string, error) { return checkDotenv(string(b)) },
			apply:   func() (string, []string, error) { return d.applySiteEnv(s) },
		})
	}

	htaccess := filepath.Join(s.DocRoot, ".htaccess")
	ht := fileSpec{
		EditableFile: EditableFile{Key: siteKey(s, "htaccess"), Title: rel(htaccess), Path: htaccess, Syntax: "apache",
			Help: "Reglas de Apache para la carpeta pública: redirecciones, reescrituras, cabeceras. Al guardar, Nido pide la portada del sitio y vuelve atrás si Apache rechaza el archivo."},
		starter: htaccessStarter,
		apply:   func() (string, []string, error) { return d.probeHtaccess(s) },
	}
	if s.Server != config.ServerApache {
		ht.Note = fmt.Sprintf("Este sitio usa %s, que no lee .htaccess: cuenta cuando vuelva a Apache.", server)
	}
	out = append(out, ht)

	userIni := filepath.Join(s.DocRoot, ".user.ini")
	ui := fileSpec{
		EditableFile: EditableFile{Key: siteKey(s, "user-ini"), Title: rel(userIni), Path: userIni, Syntax: "ini",
			Help: "Valores de PHP solo para este sitio (memory_limit, upload_max_filesize, max_execution_time…). Nido comprueba que su PHP los conoce y que se pueden cambiar por carpeta; cuentan desde la petición siguiente."},
		starter: userIniStarter,
		check: func(b []byte) ([]string, error) {
			v, err := d.sitePHP(s)
			if err != nil {
				return nil, err
			}
			return d.checkPHPValues(v, b, true)
		},
	}
	ui.apply = func() (string, []string, error) { return "PHP lo lee desde la petición siguiente.", nil, nil }
	if s.Server == config.ServerFrankenPHP {
		ui.Note = "FrankenPHP no lee .user.ini: con él, cambia los valores en el php.ini de su PHP (pestaña «PHP y Node»)."
		ui.apply = func() (string, []string, error) {
			return "Guardado, pero FrankenPHP no lee .user.ini: cuenta cuando el sitio vuelva a Apache o Nginx.", nil, nil
		}
	}
	out = append(out, ui)

	for _, srv := range []string{config.ServerApache, config.ServerNginx} {
		path := d.directivesPath(s.Name, srv)
		if srv == config.ServerNginx && s.Server != srv && !fileExists(path) {
			if _, err := d.nginxVersion(); err != nil {
				continue // Nginx is not installed
			}
		}
		spec := fileSpec{
			EditableFile: EditableFile{Key: siteKey(s, srv), Title: "Directivas de " + serverNames[srv], Path: path, Syntax: srv, Help: directivesHelp[srv]},
			starter:      fmt.Sprintf(directivesStarter[srv], s.Host),
			apply:        func() (string, []string, error) { return d.applySiteDirectives(s, srv) },
		}
		if s.Server != srv {
			spec.Note = fmt.Sprintf("Cuentan cuando el sitio use %s (ahora usa %s); Nido las comprueba igual al guardar.", serverNames[srv], server)
		}
		out = append(out, spec)
	}
	return out
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// checkDotenv finds what makes phpdotenv (Laravel) or Symfony's Dotenv refuse a .env, or read it
// differently from how it looks: a line without =, a bad name, quotes that never close, text after
// the closing quote, an unquoted value with spaces. Repeated names are only a warning.
func checkDotenv(text string) (warnings []string, err error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	seen := map[string]int{}
	var problems []string
	for i := 0; i < len(lines); i++ {
		n := i + 1
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		name = strings.TrimSpace(name)
		if !ok {
			problems = append(problems, fmt.Sprintf("línea %d: falta el signo = en «%s»", n, clip(line, 40)))
			continue
		}
		if !envName.MatchString(name) {
			problems = append(problems, fmt.Sprintf("línea %d: «%s» no es un nombre válido (letras, números y _, sin espacios)", n, clip(name, 40)))
			continue
		}
		if first, dup := seen[name]; dup {
			warnings = append(warnings, fmt.Sprintf("%s aparece dos veces (líneas %d y %d)", name, first, n))
		} else {
			seen[name] = n
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if q := value[0]; q == '"' || q == '\'' {
			// The value ends at the matching quote, maybe some lines below (multi-line values).
			rest, j := value[1:], i
			end := closingQuote(rest, q)
			for end < 0 && j+1 < len(lines) {
				j++
				rest += "\n" + lines[j]
				end = closingQuote(rest, q)
			}
			if end < 0 {
				problems = append(problems, fmt.Sprintf("línea %d: las comillas del valor de %s no se cierran", n, name))
				break
			}
			if after := strings.TrimSpace(rest[end+1:]); after != "" && !strings.HasPrefix(after, "#") {
				problems = append(problems, fmt.Sprintf("línea %d: hay texto después de las comillas de %s", j+1, name))
			}
			i = j
			continue
		}
		if c := strings.Index(value, " #"); c >= 0 {
			value = value[:c] // a comment after the value
		}
		if strings.ContainsAny(strings.TrimSpace(value), " \t") {
			problems = append(problems, fmt.Sprintf("línea %d: el valor de %s tiene espacios: ponlo entre comillas (%s=\"%s\")", n, name, name, clip(strings.TrimSpace(value), 30)))
		}
	}
	if len(problems) > 0 {
		return warnings, fmt.Errorf("el .env tiene errores:\n%s", strings.Join(problems, "\n"))
	}
	return warnings, nil
}

// closingQuote finds the quote that ends a quoted value, after the opening one; inside double
// quotes a backslash escapes the next character. -1 when it does not close.
func closingQuote(s string, q byte) int {
	for i := 0; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++
		case s[i] == q:
			return i
		}
	}
	return -1
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// applySiteEnv makes a new .env count. Laravel reads it on every request unless its configuration
// is cached (config:clear takes care of that); the site's processes read it once, so the running
// ones restart. When Laravel can no longer read the file, it gets the previous one back.
func (d *Daemon) applySiteEnv(s sites.Site) (string, []string, error) {
	var done, warnings []string
	if s.Kind == "laravel" && fileExists(filepath.Join(s.Folder, "vendor", "autoload.php")) {
		cached := fileExists(filepath.Join(s.Folder, "bootstrap", "cache", "config.php"))
		cmd := "env" // starts Laravel, which reads .env
		if cached {
			cmd = "config:clear"
		}
		out, err := d.artisan(s, cmd)
		switch {
		case err != nil && (strings.Contains(out, "environment file is invalid") || strings.Contains(out, "Failed to parse dotenv")):
			return "", nil, fmt.Errorf("Laravel no puede leer el .env: %s", procutil.LastLines(out, 3))
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("php artisan %s falló: %s", cmd, procutil.LastLines(out, 2)))
		case cached:
			done = append(done, "borró la caché de configuración (config:clear)")
		}
	}
	var restarted []string
	for _, id := range projectProcesses {
		name := processService(id, s.Name)
		if d.sup.PID(name) == 0 {
			continue
		}
		d.stopProcess(s, id)
		err := d.startProcess(s, id)
		d.setProblem(name, err)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s no volvió a arrancar: %v", processLabels[id], err))
			continue
		}
		restarted = append(restarted, processLabels[id])
	}
	if len(restarted) > 0 {
		done = append(done, "reinició "+strings.Join(restarted, ", "))
		d.changed()
	}
	if len(done) == 0 {
		return "El sitio lo lee desde la petición siguiente.", warnings, nil
	}
	return "Nido " + strings.Join(done, " y ") + ": el sitio ya lo usa.", warnings, nil
}

// artisan runs an Artisan command of a site with its PHP and Nido's php.ini.
func (d *Daemon) artisan(s sites.Site, args ...string) (string, error) {
	v, err := d.sitePHP(s)
	if err != nil {
		return "", err
	}
	ini, err := php.WriteIni(d.env.Home, v, d.env.IniOptions())
	if err != nil {
		return "", err
	}
	env := []string{"PHPRC=" + filepath.Dir(ini), d.env.ChildPATH()}
	return procutil.OutputEnv(time.Minute, s.Folder, env, filepath.Join(v.Path, "php.exe"), append([]string{"-c", ini, "artisan"}, args...)...)
}

// probeHtaccess asks Apache for the site's home page. Apache reads .htaccess on every request, so a
// broken one only shows as an error 500 with its reason in Apache's log.
func (d *Daemon) probeHtaccess(s sites.Site) (string, []string, error) {
	if s.Server != config.ServerApache {
		return "Guardado; cuenta cuando el sitio vuelva a Apache.", nil, nil
	}
	if d.sup.PID(apache.ServiceName) == 0 {
		return "Guardado; Apache lo leerá cuando arranque.", nil, nil
	}
	logPath := d.env.Home.Logs("apache-error.log")
	var from int64
	if st, err := os.Stat(logPath); err == nil {
		from = st.Size()
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", s.Port), nil)
	if err != nil {
		return "", nil, err
	}
	req.Host = s.Host
	if s.HTTPS {
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "Guardado, aunque Nido no pudo pedir la portada para probarlo: " + err.Error(), nil, nil
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		return fmt.Sprintf("Apache lo acepta: la portada responde %d.", resp.StatusCode), nil, nil
	}
	// Apache logs the reason as it answers; a moment later it is surely written.
	for try := 0; try < 3; try++ {
		for _, line := range linesSince(logPath, from) {
			if _, reason, ok := strings.Cut(line, ".htaccess: "); ok {
				return "", nil, fmt.Errorf("Apache rechaza el .htaccess: %s", strings.TrimSpace(reason))
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "Guardado. La portada responde 500, pero no por el .htaccess: mira el registro de PHP o de Laravel.", nil, nil
}

// linesSince reads the lines a log got after offset.
func linesSince(path string, offset int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}

// applySiteDirectives checks a site's own directives with the server they are for, and applies them
// when the site uses that server. A site on another server is checked in a copy of the server's
// configuration that includes it.
func (d *Daemon) applySiteDirectives(s sites.Site, server string) (string, []string, error) {
	label := serverNames[server]
	switch server {
	case config.ServerApache:
		opt, err := d.apacheOptions()
		if err != nil {
			return "", nil, err
		}
		if s.Server != server {
			opt.Sites = append(opt.Sites, d.apacheSite(s))
			conf := d.env.Home.Tmp("apache-prueba.conf")
			defer os.Remove(conf)
			if err := apache.WriteTo(opt, conf); err != nil {
				return "", nil, err
			}
			if err := apache.Test(opt.Version, conf); err != nil {
				return "", nil, err
			}
			return "Apache las acepta. Cuentan cuando el sitio use Apache.", nil, nil
		}
		conf, err := apache.Write(opt)
		if err == nil {
			err = apache.Test(opt.Version, conf)
		}
		if err != nil {
			return "", nil, err
		}
		pid := d.sup.PID(apache.ServiceName)
		if pid == 0 {
			return "Apache las acepta; cuentan cuando arranque.", nil, nil
		}
		if err := apache.GracefulRestart(pid); err != nil {
			return "", nil, err
		}
	case config.ServerNginx:
		opt, err := d.nginxOptions()
		if err != nil {
			return "", nil, err
		}
		if s.Server != server {
			opt.Sites = append(opt.Sites, d.nginxSite(s))
			conf := d.env.Home.Tmp("nginx-prueba.conf")
			defer os.Remove(conf)
			if err := nginx.WriteTo(opt, conf); err != nil {
				return "", nil, err
			}
			if err := nginx.TestFile(d.env.Home, opt.Version, conf); err != nil {
				return "", nil, err
			}
			return "Nginx las acepta. Cuentan cuando el sitio use Nginx.", nil, nil
		}
		_, err = nginx.Write(opt)
		if err == nil {
			err = nginx.Test(d.env.Home, opt.Version)
		}
		if err != nil {
			return "", nil, err
		}
		if d.sup.PID(nginx.ServiceName) == 0 {
			return "Nginx las acepta; cuentan cuando arranque.", nil, nil
		}
		if err := nginx.Reload(d.env.Home, opt.Version); err != nil {
			return "", nil, err
		}
	default:
		return "", nil, fmt.Errorf("servidor desconocido %q", server)
	}
	proxy.ForgetConnections() // the next request finds the new configuration
	return label + " las acepta y ya las aplicó, sin cortar las peticiones.", nil, nil
}

// ---- php.ini of each PHP series ----

const phpIniStarter = `; Valores de PHP que ganan a los de Nido. Quita el ; de los que quieras cambiar:
; memory_limit = 1G
; upload_max_filesize = 1G
; post_max_size = 1G
; max_execution_time = 600
; date.timezone = "UTC"
; opcache.enable = 0
`

// phpFiles are the php.ini additions: one for every version and one per installed series.
func (d *Daemon) phpFiles() []fileSpec {
	out := []fileSpec{d.phpIniFile("all")}
	seen := map[string]bool{}
	for _, v := range d.env.Reg.List(versions.PHP) {
		if series := versions.MajorMinor(v.Version); !seen[series] {
			seen[series] = true
			out = append(out, d.phpIniFile(series))
		}
	}
	return out
}

// phpVersionsOf lists the installed PHP versions an addition reaches ("all": every one).
func (d *Daemon) phpVersionsOf(series string) []versions.Version {
	var out []versions.Version
	for _, v := range d.env.Reg.List(versions.PHP) {
		if series == "all" || versions.MajorMinor(v.Version) == series {
			out = append(out, v)
		}
	}
	return out
}

func (d *Daemon) phpIniFile(series string) fileSpec {
	f := EditableFile{Key: "php:" + series, Title: "php.ini de PHP " + series, Path: d.env.Home.Config("php", series+".ini"), Syntax: "ini",
		Help: fmt.Sprintf("Valores de PHP %s que ganan a los de Nido (límites, opcache, xdebug…), en la web y en la terminal. Nido comprueba que PHP arranca con ellos y reinicia sus procesos.", series)}
	if series == "all" {
		f.Title = "php.ini de todas las versiones"
		f.Help = "Valores para todas las versiones de PHP; los de cada versión ganan a estos. Nido comprueba que cada PHP arranca con ellos y reinicia sus procesos."
	}
	return fileSpec{EditableFile: f, starter: phpIniStarter,
		check: func(b []byte) ([]string, error) {
			var warnings []string
			for _, v := range d.phpVersionsOf(series) {
				w, err := d.checkPHPValues(v, b, false)
				if err != nil {
					return nil, err
				}
				warnings = append(warnings, w...)
			}
			return warnings, nil
		},
		apply: func() (string, []string, error) { return d.applyPHPIni(series) },
	}
}

// applyPHPIni writes the php.ini of the versions an addition reaches, checks that each one starts
// with it and restarts the processes that run them.
func (d *Daemon) applyPHPIni(series string) (string, []string, error) {
	list := d.phpVersionsOf(series)
	var warnings []string
	d.cfgMu.Lock()
	for _, v := range list {
		ini, err := php.WriteIni(d.env.Home, v, d.env.IniOptions())
		if err == nil {
			var w []string
			w, err = startsWith(v, ini)
			warnings = append(warnings, w...)
		}
		if err != nil {
			d.cfgMu.Unlock()
			return "", nil, err
		}
	}
	d.cfgMu.Unlock()
	var errs []string
	for _, v := range list {
		if err := d.restartPHPVersion(v); err != nil {
			errs = append(errs, err.Error())
		}
	}
	d.restartPHPProcesses()
	d.changed()
	if len(errs) > 0 {
		return "", warnings, errors.New(strings.Join(errs, "; "))
	}
	return "PHP arranca bien con estos valores y Nido ya reinició sus procesos.", warnings, nil
}

// startsWith runs a PHP version with a php.ini: an error when it does not start, warnings with what
// it complains about while starting.
func startsWith(v versions.Version, ini string) ([]string, error) {
	out, err := procutil.Output(30*time.Second, v.Path, filepath.Join(v.Path, "php.exe"), "-c", ini, "-r", "echo 'nido-ok';")
	text := strings.TrimSpace(out)
	if err != nil || !strings.HasSuffix(text, "nido-ok") {
		detail := strings.TrimSpace(strings.TrimSuffix(text, "nido-ok"))
		if detail == "" && err != nil {
			detail = err.Error()
		}
		return nil, fmt.Errorf("PHP %s no arranca con estos valores: %s", v.Version, procutil.LastLines(detail, 4))
	}
	var warnings []string
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(text, "nido-ok"), "\n") {
		// PHP may say it twice: in its log ("PHP Warning: …") and on screen ("Warning: …").
		line = strings.TrimPrefix(strings.TrimSpace(line), "PHP ")
		if line != "" && !seen[line] {
			seen[line] = true
			warnings = append(warnings, fmt.Sprintf("PHP %s: %s", v.Version, line))
		}
	}
	return warnings, nil
}

//go:embed inicheck.php
var iniCheckScript []byte

var iniErrorLine = regexp.MustCompile(`(?s)^(.*?) in .* on line (\d+)\s*$`)

// checkPHPValues asks a PHP version about some php.ini lines: whether it can read them, names it
// does not know (a typo, or an extension that is off) and, for a .user.ini (perdir), names that
// only php.ini can change.
func (d *Daemon) checkPHPValues(v versions.Version, content []byte, perdir bool) ([]string, error) {
	script := d.env.Home.Generated("php", "inicheck.php")
	if err := writeEditable(script, iniCheckScript); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(d.env.Home.Tmp(), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(d.env.Home.Tmp(), "valores-*.ini")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(content)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	ini, err := php.WriteIni(d.env.Home, v, d.env.IniOptions())
	if err != nil {
		return nil, err
	}
	args := []string{"-c", ini, script, tmp.Name()}
	if perdir {
		args = append(args, "perdir")
	}
	out, runErr := procutil.Output(30*time.Second, v.Path, filepath.Join(v.Path, "php.exe"), args...)
	var res struct {
		Error   string   `json:"error"`
		Unknown []string `json:"unknown"`
		System  []string `json:"system"`
	}
	if err := json.Unmarshal([]byte(lastJSON(out)), &res); err != nil {
		detail := procutil.LastLines(out, 3)
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
		return nil, fmt.Errorf("PHP %s no pudo revisar los valores: %s", v.Version, detail)
	}
	if res.Error != "" {
		msg := strings.TrimSpace(res.Error)
		if m := iniErrorLine.FindStringSubmatch(msg); m != nil {
			msg = "línea " + m[2] + ": " + m[1]
		}
		return nil, fmt.Errorf("PHP %s no puede leer el archivo: %s", v.Version, msg)
	}
	if len(res.System) > 0 {
		return nil, fmt.Errorf("en .user.ini no se puede cambiar %s: va en el php.ini de la versión (pestaña «PHP y Node»)", strings.Join(res.System, ", "))
	}
	var warnings []string
	for _, name := range res.Unknown {
		warnings = append(warnings, fmt.Sprintf("PHP %s no conoce «%s» y lo ignora: revisa el nombre, o activa la extensión que lo trae", v.Version, name))
	}
	return warnings, nil
}

// lastJSON is the last line of a program's output that looks like a JSON object.
func lastJSON(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") {
			return l
		}
	}
	return ""
}

// ---- my.ini of each MySQL or MariaDB instance ----

const myIniStarter = `# Ajustes que se suman a los de Nido en [mysqld]. Quita el # de los que quieras cambiar:
# innodb_buffer_pool_size = 512M
# max_connections = 300
# max_allowed_packet = 512M
# sql_mode = "STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION"
`

// serviceFile is the my.ini addition of a MySQL or MariaDB instance.
func (d *Daemon) serviceFile(key string) (fileSpec, error) {
	kind, series, err := ParseInstanceKey(key)
	if err != nil {
		return fileSpec{}, err
	}
	if kind != versions.MySQL && kind != versions.MariaDB {
		return fileSpec{}, fmt.Errorf("%s todavía no tiene un archivo de configuración editable en Nido", kind.Name())
	}
	d.cfgMu.Lock()
	r, err := d.runnerFor(kind, series)
	d.cfgMu.Unlock()
	if err != nil {
		return fileSpec{}, err
	}
	inst, ok := r.(*mysql.Instance)
	if !ok {
		return fileSpec{}, fmt.Errorf("%s no es una instancia de MySQL", key)
	}
	name := kind.Name() + " " + series
	// Whether it ran before the change, so going back after a failed restart starts it again.
	running := d.sup.PID(inst.Key()) != 0
	path := d.env.Home.Config(inst.Key() + ".ini")
	return fileSpec{
		EditableFile: EditableFile{Key: "service:" + key, Title: "my.ini de " + name, Path: path, Syntax: "ini",
			Help: fmt.Sprintf("Ajustes de %s que se suman a los de Nido en [mysqld] (innodb_buffer_pool_size, max_connections, sql_mode…). Nido comprueba que el servidor los acepta y, si está en marcha, lo reinicia con ellos.", name)},
		starter: myIniStarter,
		apply: func() (string, []string, error) {
			added, _ := os.ReadFile(path)
			return d.applyMyIni(inst, running, iniNames(string(added)))
		},
	}, nil
}

// applyMyIni writes my.ini with the addition, asks the server whether it accepts it and restarts
// the instance with it when it was running. Only the warnings about the user's settings are kept
// (the server also warns about some of Nido's).
func (d *Daemon) applyMyIni(inst *mysql.Instance, running bool, names []string) (string, []string, error) {
	if err := inst.WriteIni(); err != nil {
		return "", nil, err
	}
	warnings, err := inst.CheckIni()
	warnings = aboutNames(warnings, names)
	if err != nil {
		return "", warnings, err
	}
	if !running {
		return "El servidor acepta los ajustes; cuentan cuando arranque.", warnings, nil
	}
	key := inst.Key()
	d.cfgMu.Lock()
	err = d.restartDB(key)
	d.cfgMu.Unlock()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err = d.sup.WaitRunning(ctx, key)
		cancel()
	}
	d.changed()
	if err != nil {
		return "", warnings, fmt.Errorf("%s no arrancó con los ajustes nuevos: %v", key, err)
	}
	return "El servidor acepta los ajustes y ya se reinició con ellos.", warnings, nil
}

// iniNames lists the setting names of an ini text, lower case and with - and _ made equal.
func iniNames(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.ContainsRune("#;[", rune(line[0])) {
			continue
		}
		name, _, _ := strings.Cut(line, "=")
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, strings.ReplaceAll(strings.ToLower(name), "-", "_"))
		}
	}
	return out
}

// aboutNames keeps the messages that mention one of names.
func aboutNames(messages, names []string) []string {
	var out []string
	for _, m := range messages {
		norm := strings.ReplaceAll(strings.ToLower(m), "-", "_")
		for _, n := range names {
			if strings.Contains(norm, n) {
				out = append(out, m)
				break
			}
		}
	}
	return out
}
