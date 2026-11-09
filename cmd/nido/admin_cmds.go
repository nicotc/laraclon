package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/nicotc/laraclon/internal/app"
	"github.com/nicotc/laraclon/internal/ca"
	"github.com/nicotc/laraclon/internal/dns"
	"github.com/nicotc/laraclon/internal/vcredist"
)

// cmdAdminSetup does, in one go and as administrator, everything that otherwise asks for permission
// one prompt at a time: the Visual C++ runtime, trusting the local CA (for the whole computer, so
// Windows asks nothing) and the wildcard DNS of the local domain (so the hosts file is never needed).
// The installer runs it once, already elevated; each step reports its own result and a failing one
// does not stop the others.
func cmdAdminSetup(ctx context.Context, env *app.Env) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("'nido admin-setup' necesita permisos de administrador (lo ejecuta el instalador)")
	}
	var failed []string
	step := func(name string, err error) {
		if err != nil {
			failed = append(failed, name)
			fmt.Printf("  ✗ %s: %v\n", name, err)
		} else {
			fmt.Printf("  ✓ %s\n", name)
		}
	}
	fmt.Println("Nido: preparando Windows (una sola vez, como administrador)")

	if len(vcredist.Missing()) > 0 || vcredist.Older("14.40") {
		step("runtime de Visual C++ (Microsoft)", vcredist.Install(ctx, env.Home, "", nil, nil))
	} else {
		step("runtime de Visual C++ ya instalado ("+vcredist.Version()+")", nil)
	}

	authority, _, err := ca.LoadOrCreate(env.Home.SSL())
	if err == nil {
		err = authority.TrustMachine()
	}
	if err == nil {
		err = env.RefreshCABundle(authority, true)
	}
	step("CA local de confianza para todo el equipo", err)

	err = dns.Apply(env.Cfg.TLD, true)
	if err == nil {
		env.Cfg.DNS.Enabled = true
		err = env.SaveConfig()
	}
	step("DNS comodín: todo *."+env.Cfg.TLD+" llega a este equipo, sin tocar hosts", err)

	if len(failed) > 0 {
		return fmt.Errorf("no se pudo: %s (Nido lo pedirá desde el panel cuando haga falta)", strings.Join(failed, ", "))
	}
	return nil
}

// cmdAdminCleanup undoes cmdAdminSetup for the uninstaller: the DNS rule and the CA in the computer's
// trusted roots. The Visual C++ runtime stays (other programs use it).
func cmdAdminCleanup(env *app.Env) error {
	var errs []string
	if err := dns.Apply(env.Cfg.TLD, false); err != nil {
		errs = append(errs, err.Error())
	}
	if _, err := os.Stat(env.Home.SSL("nido-ca.crt")); err == nil {
		if authority, _, err := ca.LoadOrCreate(env.Home.SSL()); err == nil {
			authority.UntrustMachine() // it may not be there: nothing to report
			authority.Untrust()
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
