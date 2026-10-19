package install

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nicotc/laraclon/internal/versions"
)

func TestPickMySQLSeries(t *testing.T) {
	by := map[string][]string{
		"26.7": {"26.7.0"}, "9.7": {"9.7.0", "9.7.2"}, "9.6": {"9.6.1"}, "9.1": {"9.1.0"},
		"8.4": {"8.4.11"}, "8.3": {"8.3.0"}, "8.0": {"8.0.46"}, "5.7": {"5.7.44"}, "5.6": {"5.6.51"},
	}
	got := pickMySQLSeries(by)
	want := []string{"26.7", "9.7", "8.4", "8.0", "5.7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSupportedMariaDB(t *testing.T) {
	all := []mariadbMajor{
		{ID: "13.2", Status: "Preview"}, {ID: "13.0", Status: "Stable", Support: "Rolling"},
		{ID: "11.8", Status: "Stable", Support: "Long Term Support", EOL: "2030-02-13"},
		{ID: "10.11", Status: "Stable", Support: "Long Term Support", EOL: "2028-02-16"},
		{ID: "10.6", Status: "Stable", Support: "Long Term Support", EOL: "2026-07-06"},
	}
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	var ids []string
	for _, m := range supportedMariaDB(all, now) {
		ids = append(ids, m.ID)
	}
	if want := []string{"13.0", "11.8", "10.11"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("got %v, want %v (no previews, nothing past its end of life)", ids, want)
	}
}

func TestMariaDBWindowsZipFromTheAPI(t *testing.T) {
	// Trimmed answer of https://downloads.mariadb.org/rest-api/mariadb/11.8/latest/
	const answer = `{"releases": {"11.8.9": {"release_id": "11.8.9", "files": [
	  {"file_name": "yum/", "checksum": {"sha256sum": null}, "file_download_url": "http://downloads.mariadb.org/rest-api/mariadb/11.8.9/yum/"},
	  {"file_name": "mariadb-11.8.9-winx64.msi", "checksum": {"sha256sum": "aa"}, "file_download_url": "http://downloads.mariadb.org/rest-api/mariadb/11.8.9/mariadb-11.8.9-winx64.msi"},
	  {"file_name": "mariadb-11.8.9-winx64.zip", "checksum": {"sha256sum": "830c46727d9278eae212ae3eca44eeb9e71b2a68704e95f344a64fba7b1963f5"},
	   "file_download_url": "http://downloads.mariadb.org/rest-api/mariadb/11.8.9/mariadb-11.8.9-winx64.zip"}]}}}`
	var rel mariadbReleases
	if err := json.Unmarshal([]byte(answer), &rel); err != nil {
		t.Fatal(err)
	}
	r, ok := mariadbWindowsZip(mariadbMajor{ID: "11.8", Support: "Long Term Support", EOL: "2030-02-13"}, rel)
	if !ok {
		t.Fatal("the zip must be found")
	}
	if r.Kind != versions.MariaDB || r.Series != "11.8" || r.Version != "11.8.9" || r.Note != "LTS hasta 2030-02" {
		t.Fatalf("got %+v", r)
	}
	if r.URL != "https://downloads.mariadb.org/rest-api/mariadb/11.8.9/mariadb-11.8.9-winx64.zip" {
		t.Fatalf("the download must use HTTPS: %s", r.URL)
	}
	if len(r.SHA256) != 64 {
		t.Fatal("the published SHA-256 must be kept")
	}
}

func TestFindDBRelease(t *testing.T) {
	list := []DBRelease{{Kind: versions.MySQL, Series: "8.4", Version: "8.4.11"}, {Kind: versions.MySQL, Series: "8.0", Version: "8.0.46"}}
	if r, err := FindDBRelease(list, "8.0"); err != nil || r.Version != "8.0.46" {
		t.Fatalf("by series: %+v %v", r, err)
	}
	if r, err := FindDBRelease(list, "8.4.11"); err != nil || r.Series != "8.4" {
		t.Fatalf("by version: %+v %v", r, err)
	}
	if _, err := FindDBRelease(list, "5.7"); err == nil {
		t.Fatal("a missing series must be reported")
	}
}
