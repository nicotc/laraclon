// Package ca is Nido's local certificate authority. It signs a certificate per site on demand.
// The CA key is stored encrypted with DPAPI, so only the Windows user who created it can use it.
// Making browsers trust the CA is an explicit, separate step (Trust), confirmed by Windows itself.
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/nicotc/laraclon/internal/config"
	"github.com/nicotc/laraclon/internal/winx"
)

// Authority holds the CA certificate and key and caches issued site certificates.
type Authority struct {
	dir     string
	cert    *x509.Certificate
	certPEM []byte
	key     *ecdsa.PrivateKey

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func (a *Authority) CertPath() string { return filepath.Join(a.dir, "nido-ca.crt") }
func (a *Authority) keyPath() string  { return filepath.Join(a.dir, "nido-ca.key.dpapi") }
func (a *Authority) CertPEM() []byte  { return a.certPEM }

// LoadOrCreate opens the CA in dir, creating a new one the first time.
func LoadOrCreate(dir string) (*Authority, bool, error) {
	a := &Authority{dir: dir, cache: map[string]*tls.Certificate{}}
	certPEM, err := os.ReadFile(a.CertPath())
	if errors.Is(err, fs.ErrNotExist) {
		if err := a.create(); err != nil {
			return nil, false, err
		}
		return a, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, false, fmt.Errorf("%s no es un certificado PEM", a.CertPath())
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, false, err
	}
	sealed, err := os.ReadFile(a.keyPath())
	if err != nil {
		return nil, false, err
	}
	der, err := winx.Unprotect(sealed)
	if err != nil {
		return nil, false, fmt.Errorf("no se pudo descifrar la clave de la CA (¿otro usuario de Windows?): %w", err)
	}
	key, err := x509.ParseECPrivateKey(der)
	if err != nil {
		return nil, false, err
	}
	a.cert, a.certPEM, a.key = cert, certPEM, key
	return a, false, nil
}

func (a *Authority) create() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	owner := "local"
	if u, err := user.Current(); err == nil {
		owner = u.Username
	}
	host, _ := os.Hostname()
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}
	ski := sha1.Sum(pub)
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: fmt.Sprintf("Nido CA local (%s en %s)", owner, host), Organization: []string{"Nido (desarrollo local)"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		SubjectKeyId:          ski[:],
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	sealed, err := winx.Protect(keyDER)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return err
	}
	if err := config.WriteFileAtomic(a.keyPath(), sealed); err != nil {
		return err
	}
	if err := config.WriteFileAtomic(a.CertPath(), certPEM); err != nil {
		return err
	}
	a.cert, a.certPEM, a.key = cert, certPEM, key
	return nil
}

func randomSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	return n
}

// Leaf returns a certificate for host (and *.host), issuing or renewing it when needed.
// The certificate and key are also written to ssl\sites so tools such as Vite can use them.
func (a *Authority) Leaf(host string) (*tls.Certificate, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.cache[host]; ok && time.Until(c.Leaf.NotAfter) > 30*24*time.Hour {
		return c, nil
	}
	certFile, keyFile := a.SiteFiles(host)
	if c, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil && time.Until(leaf.NotAfter) > 30*24*time.Hour && leaf.CheckSignatureFrom(a.cert) == nil {
			c.Leaf = leaf
			a.cache[host] = &c
			return &c, nil
		}
	}
	c, err := a.issue(host)
	if err != nil {
		return nil, err
	}
	a.cache[host] = c
	return c, nil
}

// SiteFiles are the PEM files of a site certificate.
func (a *Authority) SiteFiles(host string) (certFile, keyFile string) {
	return filepath.Join(a.dir, "sites", host+".crt"), filepath.Join(a.dir, "sites", host+".key")
}

func (a *Authority) issue(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:   randomSerial(),
		Subject:        pkix.Name{CommonName: host, Organization: []string{"Nido (desarrollo local)"}},
		NotBefore:      time.Now().Add(-time.Hour),
		NotAfter:       time.Now().AddDate(1, 0, 0),
		KeyUsage:       x509.KeyUsageDigitalSignature,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		AuthorityKeyId: a.cert.SubjectKeyId,
		DNSNames:       []string{host, "*." + host},
	}
	if host == "localhost" {
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certFile, keyFile := a.SiteFiles(host)
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return nil, err
	}
	// The chain file includes the CA so tools that do not consult Windows still see it.
	if err := config.WriteFileAtomic(certFile, append(certPEM, a.certPEM...)); err != nil {
		return nil, err
	}
	if err := config.WriteFileAtomic(keyFile, keyPEM); err != nil {
		return nil, err
	}
	c, err := tls.X509KeyPair(append(certPEM, a.certPEM...), keyPEM)
	if err != nil {
		return nil, err
	}
	c.Leaf, _ = x509.ParseCertificate(der)
	return &c, nil
}

// WriteBundle writes public roots (the Mozilla list) plus this CA, for PHP's curl/openssl settings.
func (a *Authority) WriteBundle(mozillaPEM, out string) error {
	public, _ := os.ReadFile(mozillaPEM)
	data := append([]byte{}, public...)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, []byte("\n# Nido CA local\n")...)
	data = append(data, a.certPEM...)
	return config.WriteFileAtomic(out, data)
}

// Trusted reports whether Windows already trusts the CA (verifies a fresh leaf against the system roots).
func (a *Authority) Trusted() bool {
	c, err := a.issueEphemeral("nido-comprobacion.test")
	if err != nil {
		return false
	}
	_, err = c.Verify(x509.VerifyOptions{DNSName: "nido-comprobacion.test"})
	return err == nil
}

func (a *Authority) issueEphemeral(host string) (*x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		AuthorityKeyId: a.cert.SubjectKeyId, DNSNames: []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// Trust adds the CA to the current user's trusted roots with certutil. Windows shows its own
// confirmation dialog; nothing is installed for other users and no administrator rights are used.
func (a *Authority) Trust() error {
	cmd := exec.Command("certutil", "-user", "-addstore", "Root", a.CertPath())
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Untrust removes the CA from the current user's trusted roots.
func (a *Authority) Untrust() error {
	serial := strings.ToLower(a.cert.SerialNumber.Text(16))
	cmd := exec.Command("certutil", "-user", "-delstore", "Root", serial)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
