// Package mailpit runs Mailpit, the test mail server: PHP's mail() and Laravel's smtp mailer send to
// it, and its web page shows every message.
package mailpit

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// Instance is one Mailpit series ("mailpit-1.31") with its SMTP and web ports.
type Instance struct {
	Home    paths.Home
	Name    string
	Version versions.Version
	Port    int // SMTP (1025)
	WebPort int // web page (8025)
}

func (i Instance) Key() string       { return i.Name }
func (i Instance) DataDir() string   { return i.Home.Data(i.Name) }
func (i Instance) LogPath() string   { return i.Home.Logs(i.Name + ".log") }
func (i Instance) Ports() []int      { return []int{i.Port, i.WebPort} }
func (i Instance) Prepare() error    { return nil }
func (i Instance) Initialize() error { return os.MkdirAll(i.DataDir(), 0o755) }

// Spec describes Mailpit. It accepts any SMTP login, so every project's settings just work.
func (i Instance) Spec() supervisor.Spec {
	return supervisor.Spec{
		Name: i.Name,
		Exe:  filepath.Join(i.Version.Path, "mailpit.exe"),
		Args: []string{
			"--smtp", fmt.Sprintf("127.0.0.1:%d", i.Port),
			"--listen", fmt.Sprintf("127.0.0.1:%d", i.WebPort),
			"--database", filepath.Join(i.DataDir(), "mailpit.db"),
			"--smtp-auth-accept-any", "--smtp-auth-allow-insecure",
		},
		Dir:         i.Version.Path,
		LogPath:     i.LogPath(),
		Port:        i.WebPort,
		Restart:     true,
		StopTimeout: 10 * time.Second,
	}
}
