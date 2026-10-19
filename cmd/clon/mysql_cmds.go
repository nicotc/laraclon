package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/laragon"
	"github.com/nicotc/laraclon/internal/versions"
)

const mysqlUsage = `
  clon mysql laragon-data [ruta]    usa tus bases de datos de Laragon (misma versión de MySQL, sin copiar)
  clon mysql own-data               vuelve a la carpeta de datos propia de clon
`

func cmdMySQL(env *app.Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("uso:%s", mysqlUsage)
	}
	if _, err := app.Connect(env.Home.Root); err == nil {
		return fmt.Errorf("cierra clon primero ('clon quit'): la carpeta de datos se cambia con MySQL detenido")
	}
	switch args[0] {
	case "laragon-data":
		root := laragon.DefaultRoot
		if len(args) > 1 {
			root = args[1]
		}
		dataRoot, mysqlFolder, err := laragonMySQL(root)
		if err != nil {
			return err
		}
		v, ok := env.Reg.Find(versions.MySQL, versionFromFolder(mysqlFolder))
		if !ok {
			return fmt.Errorf("la versión de MySQL de Laragon (%s) no está registrada; ejecuta 'clon import laragon'", mysqlFolder)
		}
		dir := filepath.Join(dataRoot, "mysql-8")
		if versions.Compare(v.Version, "8.0") < 0 {
			dir = filepath.Join(dataRoot, "mysql")
		}
		if _, err := os.Stat(filepath.Join(dir, "mysql")); err != nil {
			return fmt.Errorf("%s no parece una carpeta de datos de MySQL", dir)
		}
		// The instance of that series opens Laragon's folder, pinned to Laragon's exact version,
		// and becomes the default MySQL.
		series := versions.MajorMinor(v.Version)
		svc := env.Cfg.Service(string(versions.MySQL))
		inst, ok := svc.Instances[series]
		if !ok {
			inst.Port = app.DBStandardPort
		}
		inst.DataDir, inst.Version = dir, v.Version
		svc.Instances[series] = inst
		svc.Default = series
		env.Cfg.SetService(string(versions.MySQL), svc)
		if err := env.SaveConfig(); err != nil {
			return err
		}
		key := app.InstanceKey(versions.MySQL, series)
		fmt.Printf("%s (MySQL %s) usará tus bases de datos de Laragon: %s\n", key, v.Version, dir)
		fmt.Println("No se copia nada. Nunca corren los dos a la vez: si el MySQL de Laragon está abierto, clon no arranca el suyo.")
		if inst.Port != app.DBStandardPort {
			fmt.Printf("Para que tus proyectos no cambien su .env, usa el puerto de Laragon (con Laragon cerrado): clon service port %s %d\n", key, app.DBStandardPort)
		}
		return nil
	case "own-data":
		svc := env.Cfg.Service(string(versions.MySQL))
		for series, inst := range svc.Instances {
			if inst.DataDir != "" {
				inst.DataDir, inst.Version = "", ""
				svc.Instances[series] = inst
				fmt.Printf("%s vuelve a su propia carpeta de datos (%s).\n", app.InstanceKey(versions.MySQL, series), env.Home.Data(app.InstanceKey(versions.MySQL, series)))
			}
		}
		env.Cfg.SetService(string(versions.MySQL), svc)
		return env.SaveConfig()
	}
	return fmt.Errorf("uso:%s", mysqlUsage)
}

// laragonMySQL reads DataDir and the active MySQL folder from laragon.ini.
func laragonMySQL(root string) (dataRoot, mysqlFolder string, err error) {
	f, err := os.Open(filepath.Join(root, "usr", "laragon.ini"))
	if err != nil {
		return "", "", fmt.Errorf("no se encontró laragon.ini en %s", root)
	}
	defer f.Close()
	section := ""
	dataRoot = filepath.Join(root, "data")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch {
		case section == "preferences" && k == "datadir" && v != "":
			dataRoot = v
		case section == "mysql" && k == "version":
			mysqlFolder = v
		}
	}
	if mysqlFolder == "" {
		return "", "", fmt.Errorf("laragon.ini no indica la versión de MySQL")
	}
	return dataRoot, mysqlFolder, sc.Err()
}

// versionFromFolder turns "mysql-8.0.30-winx64" into "8.0.30".
func versionFromFolder(folder string) string {
	for _, part := range strings.Split(folder, "-") {
		if len(part) > 0 && part[0] >= '0' && part[0] <= '9' && strings.Count(part, ".") >= 1 {
			return part
		}
	}
	return folder
}
