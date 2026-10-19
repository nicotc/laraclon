package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nicotc/laraclon/internal/paths"
	"github.com/nicotc/laraclon/internal/supervisor"
	"github.com/nicotc/laraclon/internal/versions"
)

// On a fresh install nothing of most kinds is installed; the panel counts their instances and broke
// with "Cannot read properties of null (reading 'length')" when they travelled as null.
func TestEmptyListsTravelAsArrays(t *testing.T) {
	d := &Daemon{env: &Env{Home: paths.Home{Root: t.TempDir()}, Reg: &versions.Registry{}}, sup: supervisor.New()}
	data, err := json.Marshal(d.dbGroups(nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") || !strings.Contains(string(data), `"instances":[]`) {
		t.Fatalf("got %s", data)
	}
	if got, _ := json.Marshal(orEmpty([]string(nil))); string(got) != "[]" {
		t.Fatalf("got %s", got)
	}
}
