package install

import (
	"encoding/json"
	"reflect"
	"strings"
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

func TestPickNginx(t *testing.T) {
	rel := func(tag, zip string) githubRelease {
		r := githubRelease{Tag: tag}
		r.Assets = append(r.Assets, struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		}{Name: zip, URL: "https://github.com/nginx/nginx/releases/download/" + tag + "/" + zip, Digest: "sha256:" + strings.Repeat("a", 64), Size: 2800000})
		return r
	}
	got := pickNginx([]githubRelease{
		rel("release-1.31.7", "nginx-1.31.7.zip"), rel("release-1.31.6", "nginx-1.31.6.zip"),
		rel("release-1.30.6", "nginx-1.30.6.zip"), rel("release-1.30.5", "nginx-1.30.5.zip"),
		rel("release-1.29.9", "nginx-1.29.9.zip"), rel("release-1.28.3", "nginx-1.28.3.zip"),
		rel("release-1.27.2", "nginx-1.27.2.zip"),
	})
	var desc []string
	for _, r := range got {
		desc = append(desc, r.Version+" "+r.Note)
	}
	want := []string{"1.31.7 mainline", "1.30.6 estable", "1.28.3 estable anterior"}
	if !reflect.DeepEqual(desc, want) {
		t.Fatalf("got %v, want %v", desc, want)
	}
	if got[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatal("the SHA-256 from GitHub must be kept")
	}
}

func TestPickFrankenPHP(t *testing.T) {
	rel := func(tag string, prerelease bool) githubRelease {
		r := githubRelease{Tag: tag, Prerelease: prerelease}
		r.Assets = append(r.Assets, struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		}{Name: frankenphpAsset, URL: "https://github.com/php/frankenphp/releases/download/" + tag + "/" + frankenphpAsset, Digest: "sha256:" + strings.Repeat("b", 64)})
		return r
	}
	got := pickFrankenPHP([]githubRelease{
		rel("v1.14.0-rc.1", true), rel("v1.13.1", false), rel("v1.13.0", false), rel("v1.12.7", false), rel("v1.11.3", false),
	})
	var desc []string
	for _, r := range got {
		desc = append(desc, r.Version+" "+r.Note)
	}
	if want := []string{"1.13.1 la más reciente", "1.12.7 serie anterior"}; !reflect.DeepEqual(desc, want) {
		t.Fatalf("got %v, want %v (no prereleases)", desc, want)
	}
}

func TestFindRelease(t *testing.T) {
	list := []Release{{Kind: versions.MySQL, Series: "8.4", Version: "8.4.11"}, {Kind: versions.MySQL, Series: "8.0", Version: "8.0.46"}}
	if r, err := FindRelease(list, "8.0"); err != nil || r.Version != "8.0.46" {
		t.Fatalf("by series: %+v %v", r, err)
	}
	if r, err := FindRelease(list, "8.4.11"); err != nil || r.Series != "8.4" {
		t.Fatalf("by version: %+v %v", r, err)
	}
	if _, err := FindRelease(list, "5.7"); err == nil {
		t.Fatal("a missing series must be reported")
	}
	if r, err := FindRelease(list, "latest"); err != nil || r.Series != "8.4" {
		t.Fatalf("latest must be the first (newest) entry: %+v %v", r, err)
	}
}
