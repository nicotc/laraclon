package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/vcredist"
)

// cmdVCRedist reports or installs the Visual C++ 2015-2022 x64 runtime.
// `clon vcredist install [instalador.exe]` uses a local installer when given (it must still carry
// Microsoft's signature), otherwise it downloads the official one.
func cmdVCRedist(ctx context.Context, env *app.Env, args []string) error {
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "status":
		if missing := vcredist.Missing(); len(missing) > 0 {
			fmt.Printf("Falta el runtime de Visual C++ 2015-2022 x64 (%s).\n", strings.Join(missing, ", "))
			fmt.Println("Sin él no arrancan PHP, Apache ni MySQL. Instálalo con 'clon vcredist install'.")
			return nil
		}
		fmt.Println("Runtime de Visual C++ 2015-2022 x64 instalado:", vcredist.Version())
		return nil
	case "install":
		installer := ""
		if len(args) > 1 {
			installer = args[1]
			fmt.Println("Instalando el runtime de Visual C++ desde", installer, "(Windows pedirá permiso de administrador)…")
		} else {
			fmt.Println("Descargando el runtime de Visual C++ de Microsoft (Windows pedirá permiso de administrador)…")
		}
		if err := vcredist.Install(ctx, env.Home, installer, printProgress(), vcStatus(installer == "")); err != nil {
			fmt.Println()
			return err
		}
		fmt.Println("\nRuntime de Visual C++ instalado:", vcredist.Version())
		return nil
	}
	return fmt.Errorf("uso: clon vcredist status | install [instalador.exe]")
}

// vcStatus prints the installer's phase lines; after a download it first ends the progress line.
func vcStatus(downloaded bool) func(string) {
	first := true
	return func(line string) {
		if first && downloaded {
			fmt.Println()
		}
		first = false
		fmt.Println(line)
	}
}
