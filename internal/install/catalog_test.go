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

func TestPickApacheFromTheDownloadPage(t *testing.T) {
	// Trimmed from https://www.apachelounge.com/download/
	const page = `<a href="https://aka.ms/vc14/vc_redist.x64.exe" target="_blank">vc_redist_x64</a>
	<a href="/download/VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip">httpd-2.4.69-261002-Win64-VS18.zip</a>
	<a href="/download/VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip.asc">PGP</a>
	<a href="/download/VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip.txt">Checksums</a>
	<a href="/download/VS18/binaries/httpd-2.4.69-261002-win32-vs18.zip">httpd-2.4.69-261002-win32-vs18.zip</a>
	<a href="/download/VS18/modules/mod_fcgid-2.3.10-win64-VS18.zip">mod_fcgid</a>`
	r, ok := pickApache(page)
	if !ok {
		t.Fatal("the 64-bit build must be found")
	}
	if r.Kind != versions.Apache || r.Series != "2.4" || r.Version != "2.4.69" || r.Note != "compilación VS18" {
		t.Fatalf("got %+v", r)
	}
	if r.URL != "https://www.apachelounge.com/download/VS18/binaries/httpd-2.4.69-261002-Win64-VS18.zip" {
		t.Fatalf("url %s", r.URL)
	}
	if !reflect.DeepEqual(r.Skip, []string{"Apache24/manual/"}) {
		t.Fatalf("skip %v", r.Skip)
	}
	if _, ok := pickApache(`<a href="/download/VS18/binaries/httpd-2.4.69-261002-win32-vs18.zip">32 bits</a>`); ok {
		t.Fatal("a 32-bit build must not be offered")
	}
}

func TestApacheChecksumFile(t *testing.T) {
	// Layout of httpd-2.4.69-261002-Win64-VS18.zip.txt (Windows line endings).
	const sums = "Checksums created with GPGHash by SmartJuwel\r\n\r\n" +
		"SHA1-Checksum for: httpd-2.4.69-261002-Win64-VS18.zip:\r\nD7B557581787727CA222C6A5FF0E9E6FA6029893\r\n\r\n" +
		"SHA256-Checksum for: httpd-2.4.69-261002-Win64-VS18.zip:\r\nCA272E0B75ABAD78B1E4029A0753B20FF2232D2D857E8EEBEEA5D469C5776977\r\n\r\n" +
		"SHA512-Checksum for: httpd-2.4.69-261002-Win64-VS18.zip:\r\nB5198796E28FD985225DF36CB3E65226A423BB48DF5B111C91439C59D558FD9DB1868F838C927E79B7A30B7B0C1C6FA0E4BEA767A3908A5187745158E22E9269\r\n"
	m := apacheSumRe.FindStringSubmatch(sums)
	if m == nil || m[1] != "CA272E0B75ABAD78B1E4029A0753B20FF2232D2D857E8EEBEEA5D469C5776977" {
		t.Fatalf("got %v", m)
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

func TestPickMongoDB(t *testing.T) {
	const doc = `{"versions": [
	  {"version": "9.0.4", "production_release": true, "downloads": [
	    {"target": "windows", "arch": "x86_64", "edition": "base", "archive": {"url": "https://fastdl.mongodb.org/windows/mongodb-windows-x86_64-9.0.4.zip", "sha256": "1e6234c8c33e75de3659a7e1104cb12f1cb68ae41cba60b625537c1ed9256d43"}},
	    {"target": "ubuntu2404", "arch": "x86_64", "edition": "base", "archive": {"url": "https://x/linux.tgz", "sha256": "1e6234c8c33e75de3659a7e1104cb12f1cb68ae41cba60b625537c1ed9256d43"}}]},
	  {"version": "9.0.2", "production_release": true, "downloads": [{"target": "windows", "arch": "x86_64", "edition": "base", "archive": {"url": "https://x/9.0.2.zip", "sha256": "2cabba2e80b90bcd1c1096845711975199104148c849a007984cb3ee4d237406"}}]},
	  {"version": "9.1.0-rc0", "production_release": false, "downloads": []},
	  {"version": "8.3.13", "production_release": true, "downloads": [{"target": "windows", "arch": "x86_64", "edition": "base", "archive": {"url": "https://x/8.3.13.zip", "sha256": "61fcc2b2ac3a295ba2d8c80f678f01502b96647d835f0159b694c507035b5238"}}]},
	  {"version": "8.0.34", "production_release": true, "downloads": [{"target": "windows", "arch": "x86_64", "edition": "base", "archive": {"url": "https://x/8.0.34.zip", "sha256": "61fcc2b2ac3a295ba2d8c80f678f01502b96647d835f0159b694c507035b5238"}}]},
	  {"version": "7.0.45", "production_release": true, "downloads": [{"target": "windows", "arch": "x86_64", "edition": "base", "archive": {"url": "https://x/7.0.45.zip", "sha256": "61fcc2b2ac3a295ba2d8c80f678f01502b96647d835f0159b694c507035b5238"}}]},
	  {"version": "6.0.27", "production_release": true, "downloads": [{"target": "windows", "arch": "x86_64", "edition": "base", "archive": {"url": "https://x/6.0.27.zip", "sha256": "61fcc2b2ac3a295ba2d8c80f678f01502b96647d835f0159b694c507035b5238"}}]}
	]}`
	var full mongoFull
	if err := json.Unmarshal([]byte(doc), &full); err != nil {
		t.Fatal(err)
	}
	var desc []string
	for _, r := range pickMongoDB(full) {
		desc = append(desc, r.Version+" "+r.Note)
	}
	want := []string{"9.0.4 la más reciente", "8.0.34 versión mayor", "7.0.45 versión mayor"}
	if !reflect.DeepEqual(desc, want) {
		t.Fatalf("got %v, want %v (no rapid release older than the newest major, no release candidates)", desc, want)
	}
}

// ghAsset is the (unnamed) asset type of githubRelease.
type ghAsset = struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

func asset(name, digest string) ghAsset {
	return ghAsset{Name: name, URL: "https://example.test/" + name, Digest: digest}
}

func TestGitHubCatalogsPickTheRightAssets(t *testing.T) {
	sum := "sha256:" + strings.Repeat("c", 64)
	redis := []githubRelease{
		{Tag: "8.10.2", Assets: []ghAsset{asset("Redis-8.10.2-Windows-x64-cygwin.zip", sum), asset("Redis-8.10.2-Windows-x64-msys2.zip", sum), asset("Redis-8.10.2-Windows-x64-msys2-with-Service.zip", sum)}},
		{Tag: "8.8.3", Assets: []ghAsset{asset("Redis-8.8.3-Windows-x64-msys2.zip", sum)}},
		{Tag: "8.6.7", Assets: []ghAsset{asset("Redis-8.6.7-Windows-x64-msys2.zip", sum)}},
	}
	got := pickGitHub(redis, versions.Redis, "x", redisAsset)
	if len(got) != 2 || got[0].Version != "8.10.2" || !strings.HasSuffix(got[0].URL, "-msys2.zip") || got[1].Version != "8.8.3" {
		t.Fatalf("redis: %+v", got)
	}
	meili := []githubRelease{
		{Tag: "v1.56.0-rc.1", Prerelease: true, Assets: []ghAsset{asset("meilisearch-windows-amd64.exe", sum)}},
		{Tag: "v1.55.0", Assets: []ghAsset{asset("meilisearch-enterprise-windows-amd64.exe", sum), asset("meilisearch-windows-amd64.exe", sum)}},
		{Tag: "v1.54.3", Assets: []ghAsset{asset("meilisearch-windows-amd64.exe", sum)}},
		{Tag: "v1.54.2", Assets: []ghAsset{asset("meilisearch-windows-amd64.exe", "sha256:short")}},
	}
	got = pickGitHub(meili, versions.Meilisearch, "x", tagAsset("meilisearch-windows-amd64.exe"))
	if len(got) != 2 || got[0].Version != "1.55.0" || strings.Contains(got[0].URL, "enterprise") || got[1].Version != "1.54.3" {
		t.Fatalf("meilisearch: %+v", got)
	}
}

func TestSupportedPostgres(t *testing.T) {
	var all []pgVersion
	doc := `[{"major":"6.3","latestMinor":"2","supported":false,"eolDate":"2003-03-01"},
	  {"major":14,"latestMinor":24,"supported":true,"eolDate":"2026-11-12"},
	  {"major":"15","latestMinor":19,"supported":true,"eolDate":"2027-11-11"},
	  {"major":"16","latestMinor":"15","supported":true,"eolDate":"2028-11-09"},
	  {"major":"17","latestMinor":11,"supported":true,"eolDate":"2029-11-08"},
	  {"major":"18","latestMinor":6,"supported":true,"eolDate":"2030-11-14"},
	  {"major":"13","latestMinor":23,"supported":true,"eolDate":"2025-11-13"}]`
	if err := json.Unmarshal([]byte(doc), &all); err != nil {
		t.Fatal(err)
	}
	var majors []string
	for _, m := range supportedPostgres(all, time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)) {
		majors = append(majors, m.Major.String()+"."+m.LatestMinor.String())
	}
	if want := []string{"18.6", "17.11", "16.15", "15.19"}; !reflect.DeepEqual(majors, want) {
		t.Fatalf("got %v, want %v (numbers as text or numbers, nothing past its end of life)", majors, want)
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
