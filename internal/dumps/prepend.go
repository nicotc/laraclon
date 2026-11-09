package dumps

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/nicotc/laraclon/internal/config"
)

//go:embed prepend.php
var prependTemplate string

// Prepend is the PHP file that sends dump() and dd() to Nido; page says whether the page (or the
// terminal) still shows them too.
func Prepend(port int, page bool) string {
	r := strings.NewReplacer("__PORT__", strconv.Itoa(port), "__PAGE__", strconv.FormatBool(page))
	return r.Replace(prependTemplate)
}

// WritePrepend writes it to path (left alone when unchanged).
func WritePrepend(path string, page bool) error {
	return config.WriteFileAtomic(path, []byte(Prepend(Port, page)))
}
