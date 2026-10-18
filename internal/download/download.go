// Package download fetches release files over HTTPS, checks their SHA-256 and unpacks zip archives.
package download

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const userAgent = "clon/0.1 (+https://github.com/nicotc/laraclon)"

var client = &http.Client{Timeout: 30 * time.Minute}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

// Text downloads a small text resource.
func Text(ctx context.Context, url string) (string, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return string(b), err
}

// JSON downloads and decodes a JSON document.
func JSON(ctx context.Context, url string, v any) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(v)
}

// Progress receives bytes done and total (total is -1 when unknown).
type Progress func(done, total int64)

// File downloads url to dest. When wantSHA256 is set the file is only kept if it matches.
// It returns the SHA-256 of what was downloaded.
func File(ctx context.Context, url, dest, wantSHA256 string, progress Progress) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	resp, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(part)
				return "", werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(part)
			return "", rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(part)
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if wantSHA256 != "" && !strings.EqualFold(sum, wantSHA256) {
		os.Remove(part)
		return sum, fmt.Errorf("el SHA-256 de %s no coincide (esperado %s, obtenido %s)", filepath.Base(dest), wantSHA256, sum)
	}
	if err := os.Rename(part, dest); err != nil {
		return sum, err
	}
	return sum, nil
}

// ExtractZip unpacks zipPath into destDir. With stripTop, a single top-level folder shared by every
// entry (node-v24.11.0-win-x64\…) is removed from the paths.
func ExtractZip(zipPath, destDir string, stripTop bool) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	prefix := ""
	if stripTop {
		prefix = commonTop(r.File)
	}
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	for _, zf := range r.File {
		name := strings.TrimPrefix(strings.ReplaceAll(zf.Name, "\\", "/"), prefix)
		if name == "" {
			continue
		}
		target := filepath.Join(absDest, filepath.FromSlash(name))
		// Refuse entries that would land outside destDir ("zip slip").
		if target != absDest && !strings.HasPrefix(target, absDest+string(os.PathSeparator)) {
			return fmt.Errorf("entrada no permitida en el zip: %s", zf.Name)
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractFile(zf, target); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(zf *zip.File, target string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func commonTop(files []*zip.File) string {
	top := ""
	for _, zf := range files {
		name := strings.ReplaceAll(zf.Name, "\\", "/")
		first, _, found := strings.Cut(name, "/")
		if !found {
			return "" // a file at the archive root: nothing to strip
		}
		if top == "" {
			top = first
		} else if top != first {
			return ""
		}
	}
	if top == "" {
		return ""
	}
	return top + "/"
}
