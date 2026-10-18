package php

import (
	"fmt"
	"path/filepath"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// PoolPorts are the FastCGI ports of the global pool.
func PoolPorts(basePort, size int) []int {
	out := make([]int, size)
	for i := range out {
		out[i] = basePort + i
	}
	return out
}

// PoolName is the supervisor name of one pool process.
func PoolName(port int) string { return fmt.Sprintf("php-cgi-%d", port) }

// PoolSpecs describes the pool: php-cgi.exe on Windows serves one request at a time, so the web
// servers balance over several processes. Each one is restarted at once if it dies.
func PoolSpecs(home paths.Home, v versions.Version, ini string, ports []int) []supervisor.Spec {
	specs := make([]supervisor.Spec, 0, len(ports))
	for _, port := range ports {
		specs = append(specs, supervisor.Spec{
			Name:    PoolName(port),
			Exe:     filepath.Join(v.Path, "php-cgi.exe"),
			Args:    []string{"-b", fmt.Sprintf("127.0.0.1:%d", port), "-c", ini},
			Dir:     v.Path,
			Env:     []string{"PHP_FCGI_MAX_REQUESTS=0", "PHPRC=" + filepath.Dir(ini)},
			LogPath: home.Logs("php-cgi.log"),
			Port:    port,
			Restart: true,
		})
	}
	return specs
}
