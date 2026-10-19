package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/sites"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

func TestCheckDotenvFindsWhatBreaksIt(t *testing.T) {
	ok := "APP_NAME=Laravel\n# comentario\n\nAPP_URL=https://tienda.test\nexport DB_HOST=127.0.0.1\n" +
		"MAIL_FROM_NAME=\"${APP_NAME}\"\nAPP_KEY=base64:abc= # la clave\nCLAVE=\"una \\\"cita\\\" adentro\"\n" +
		"PRIVADA=\"-----BEGIN-----\nlinea\n-----END-----\"\nVACIA=\nSIMPLE='con espacios'\n"
	if warnings, err := checkDotenv(ok); err != nil || len(warnings) > 0 {
		t.Fatalf("un .env correcto no debe dar errores: %v %v", err, warnings)
	}
	for text, want := range map[string]string{
		"APP_NAME=Laravel\nesto no es nada\n":          "línea 2: falta el signo =",
		"APP NAME=Laravel\n":                           "«APP NAME» no es un nombre válido",
		"APP_NAME=Mi tienda\n":                         "línea 1: el valor de APP_NAME tiene espacios",
		"APP_NAME=\"Mi tienda\nAPP_ENV=local\n":        "las comillas del valor de APP_NAME no se cierran",
		"A=1\nAPP_NAME=\"Mi tienda\" sobra\nB=2\n":     "línea 2: hay texto después de las comillas de APP_NAME",
		"PRIVADA=\"uno\ndos\" basura\nAPP_ENV=local\n": "línea 2: hay texto después de las comillas de PRIVADA",
	} {
		_, err := checkDotenv(text)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("checkDotenv(%q) = %v, quiero un error con %q", text, err, want)
		}
	}
	warnings, err := checkDotenv("APP_ENV=local\nAPP_ENV=production\n")
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "APP_ENV aparece dos veces (líneas 1 y 2)") {
		t.Errorf("un nombre repetido es un aviso: %v %v", err, warnings)
	}
}

func TestFileBytesKeepsTheLineEndingsOfTheFile(t *testing.T) {
	if got := string(fileBytes("A=1\nB=2", []byte("A=0\r\nB=0\r\n"))); got != "A=1\r\nB=2\r\n" {
		t.Errorf("un archivo con CRLF sigue con CRLF: %q", got)
	}
	if got := string(fileBytes("A=1\r\nB=2\n", []byte("A=0\n"))); got != "A=1\nB=2\n" {
		t.Errorf("un archivo con LF sigue con LF: %q", got)
	}
	if got := string(fileBytes("", nil)); got != "" {
		t.Errorf("un archivo vacío sigue vacío: %q", got)
	}
}

func TestScopeOfFindsTheOwnerOfAFile(t *testing.T) {
	for key, want := range map[string]string{
		"site:tienda:env":       "site:tienda",
		"site:mi-tienda:apache": "site:mi-tienda",
		"php:8.4":               "php",
		"php:all":               "php",
		"service:mysql-8.0":     "service:mysql-8.0",
	} {
		if got := scopeOf(key); got != want {
			t.Errorf("scopeOf(%q) = %q, quiero %q", key, got, want)
		}
	}
}

func TestOnlyTheWarningsAboutTheUsersSettingsAreKept(t *testing.T) {
	names := iniNames("# comentario\ninnodb-buffer-pool-size = 0\n[client]\nmax_connections=300\n")
	if strings.Join(names, ",") != "innodb_buffer_pool_size,max_connections" {
		t.Fatalf("iniNames = %v", names)
	}
	got := aboutNames([]string{
		"option 'innodb-buffer-pool-size': unsigned value 0 adjusted to 2097152",
		"'default_authentication_plugin' is deprecated and will be removed in a future release.",
	}, names)
	if len(got) != 1 || !strings.Contains(got[0], "innodb-buffer-pool-size") {
		t.Errorf("aboutNames = %v", got)
	}
}

// fileDaemon is a Daemon with one plain PHP site on Apache (which is not running).
func fileDaemon(t *testing.T) (*Daemon, sites.Site) {
	t.Helper()
	folder := t.TempDir()
	site := sites.Site{Name: "demo", Host: "demo.test", Folder: folder, DocRoot: filepath.Join(folder, "public"), Kind: "php", Server: config.ServerApache}
	if err := os.MkdirAll(site.DocRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Daemon{env: testEnv(t), sup: supervisor.New(), sites: []sites.Site{site}}, site
}

func TestSaveFileKeepsVersionsAndRefusesOutsideChanges(t *testing.T) {
	d, site := fileDaemon(t)
	key := "site:demo:htaccess"
	f, err := d.ReadEditable(key)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Starter || f.Hash != "" || f.Path != filepath.Join(site.DocRoot, ".htaccess") || f.Title != "public/.htaccess" {
		t.Fatalf("un .htaccess que no existe se ofrece con un contenido inicial: %+v", f)
	}
	res, err := d.SaveFile(key, "Options -Indexes", "")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(f.Path); string(data) != "Options -Indexes\n" || res.File.Backups != 1 {
		t.Fatalf("guardado: %q, %d versiones anteriores", data, res.File.Backups)
	}
	// Someone else changes the file: a save with the old hash must not overwrite it.
	os.WriteFile(f.Path, []byte("Options +Indexes\n"), 0o644)
	if _, err := d.SaveFile(key, "Options -Indexes\nHeader set X-Prueba 1", hashOf([]byte("Options -Indexes\n"))); !errors.Is(err, errFileChanged) {
		t.Fatalf("debe negarse a pisar un cambio de fuera: %v", err)
	}
	f, _ = d.ReadEditable(key)
	if _, err := d.SaveFile(key, "Options +Indexes\nHeader set X-Prueba 1", f.Hash); err != nil {
		t.Fatal(err)
	}
	// Undo goes back one save at a time, down to the file not existing.
	if _, err := d.UndoFile(key); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(f.Path); string(data) != "Options +Indexes\n" {
		t.Fatalf("deshacer vuelve a la versión anterior: %q", data)
	}
	if _, err := d.UndoFile(key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Path); !os.IsNotExist(err) {
		t.Fatal("antes del primer guardado el archivo no existía")
	}
	if _, err := d.UndoFile(key); err == nil {
		t.Fatal("sin versiones anteriores, deshacer debe avisar")
	}
}

func TestSaveFileLeavesAnInvalidEnvUnwritten(t *testing.T) {
	d, site := fileDaemon(t)
	env := filepath.Join(site.Folder, ".env")
	os.WriteFile(env, []byte("APP_NAME=Laravel\r\n"), 0o644)
	f, err := d.ReadEditable("site:demo:env")
	if err != nil {
		t.Fatal(err)
	}
	if f.Content != "APP_NAME=Laravel\n" {
		t.Fatalf("el editor recibe saltos \\n: %q", f.Content)
	}
	if _, err := d.SaveFile(f.Key, "APP_NAME=Mi tienda\n", f.Hash); err == nil {
		t.Fatal("un .env inválido no se guarda")
	}
	if data, _ := os.ReadFile(env); string(data) != "APP_NAME=Laravel\r\n" || len(d.backups(f.Key)) != 0 {
		t.Fatalf("el archivo queda como estaba y sin versiones: %q", data)
	}
	if _, err := d.SaveFile(f.Key, "APP_NAME=\"Mi tienda\"\n", f.Hash); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(env); string(data) != "APP_NAME=\"Mi tienda\"\r\n" {
		t.Fatalf("guardado con los saltos CRLF que tenía: %q", data)
	}
}

func TestApplyOrRevertPutsThePreviousFileBack(t *testing.T) {
	d, _ := fileDaemon(t)
	path := filepath.Join(t.TempDir(), "apache.conf")
	os.WriteFile(path, []byte("bueno\n"), 0o644)
	var seen []string
	spec := fileSpec{EditableFile: EditableFile{Key: "prueba", Path: path}, apply: func() (string, []string, error) {
		data, _ := os.ReadFile(path)
		seen = append(seen, string(data))
		if string(data) == "malo\n" {
			return "", nil, errors.New("Apache lo rechaza")
		}
		return "aplicado", nil, nil
	}}
	os.WriteFile(path, []byte("malo\n"), 0o644)
	_, _, err := d.applyOrRevert(spec, []byte("bueno\n"), true)
	if err == nil || !strings.Contains(err.Error(), "Apache lo rechaza") || !strings.Contains(err.Error(), "dejó el archivo como estaba") {
		t.Fatalf("el error explica qué pasó: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "bueno\n" || strings.Join(seen, "|") != "malo\n|bueno\n" {
		t.Fatalf("vuelve el archivo anterior y se aplica otra vez: %q, aplicado con %q", data, seen)
	}
}

func TestBackupsKeepTheLastVersions(t *testing.T) {
	d, _ := fileDaemon(t)
	for i := 0; i < keepBackups+3; i++ {
		if err := d.keepBackup("site:demo:env", []byte{byte('a' + i)}, true); err != nil {
			t.Fatal(err)
		}
	}
	list := d.backups("site:demo:env")
	if len(list) != keepBackups {
		t.Fatalf("se guardan las últimas %d versiones, no %d", keepBackups, len(list))
	}
	if data, _ := os.ReadFile(list[len(list)-1]); string(data) != string(rune('a'+keepBackups+2)) {
		t.Errorf("la última versión es la más nueva: %q", data)
	}
}

func TestNodeForFollowsTheSiteThatHoldsTheFolder(t *testing.T) {
	root := t.TempDir()
	env := testEnv(t,
		versions.Version{Component: versions.Node, Version: "24.22.0", Path: `C:\nido\bin\node\24.22.0`},
		versions.Version{Component: versions.Node, Version: "18.20.8", Path: `C:\nido\bin\node\18.20.8`})
	env.Cfg.Roots = []string{root}
	env.Cfg.Node.Version = "24.22.0"
	env.Cfg.SetSiteNode("tienda-vieja", "18")
	for dir, want := range map[string]string{
		filepath.Join(root, "Tienda Vieja"):                    "18.20.8", // named as its site: tienda-vieja
		filepath.Join(root, "Tienda Vieja", "resources", "js"): "18.20.8",
		filepath.Join(root, "otra"):                            "24.22.0",
		root:                                                   "24.22.0",
		filepath.Join(filepath.Dir(root), "fuera", "Tienda Vieja"): "24.22.0",
	} {
		v, err := env.NodeFor(dir)
		if err != nil || v.Version != want {
			t.Errorf("NodeFor(%s) = %s, %v; quiero %s", dir, v.Version, err, want)
		}
	}
	env.Cfg.SetSiteNode("tienda-vieja", "16")
	if _, err := env.NodeFor(filepath.Join(root, "Tienda Vieja")); err == nil || !strings.Contains(err.Error(), "Node 16") {
		t.Errorf("un Node fijado que no está instalado debe decirlo: %v", err)
	}
}

func TestPinSignatureChangesWithThePinnedSites(t *testing.T) {
	list := []sites.Site{{Name: "a", Folder: `E:\www\a`}, {Name: "b", Folder: `E:\www\b`, PHP: "8.3.12"}}
	before := pinSignature(list)
	list = append(list, sites.Site{Name: "c", Folder: `E:\www\c`, Node: "18"})
	if pinSignature(list) == before {
		t.Error("un sitio nuevo con Node fijado cambia la firma")
	}
	if pinSignature(list[:1]) != "" {
		t.Error("sin versiones fijadas, la firma está vacía")
	}
}
