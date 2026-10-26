package platform

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type CertPaths struct {
	CACert     string
	ServerCert string
	ServerKey  string
	ClientCert string
	ClientKey  string
}

// EnsureDevCerts generates an ECDSA P-256 Root CA, Server certificate (localhost/127.0.0.1),
// and Client mTLS certificate (with CN="payments-svc" and SPIFFE URI SAN) in dir if they don't exist yet.
func EnsureDevCerts(dir string) (CertPaths, error) {
	paths := CertPaths{
		CACert:     filepath.Join(dir, "ca.crt"),
		ServerCert: filepath.Join(dir, "server.crt"),
		ServerKey:  filepath.Join(dir, "server.key"),
		ClientCert: filepath.Join(dir, "client.crt"),
		ClientKey:  filepath.Join(dir, "client.key"),
	}

	if _, err := os.Stat(paths.ClientKey); err == nil {
		return paths, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return paths, err
	}

	now := time.Now().Add(-1 * time.Hour)
	notAfter := now.Add(365 * 24 * time.Hour)

	// 1. Root CA
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return paths, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "go-server-dev-ca", Organization: []string{"Moonbase Lab"}},
		NotBefore:             now,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return paths, err
	}
	if err := writePEM(paths.CACert, "CERTIFICATE", caDER, 0o644); err != nil {
		return paths, err
	}

	// 2. Server Certificate (SAN: localhost, 127.0.0.1, ::1)
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return paths, err
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caTmpl, &srvKey.PublicKey, caKey)
	if err != nil {
		return paths, err
	}
	if err := writeKeyPair(paths.ServerCert, paths.ServerKey, srvDER, srvKey); err != nil {
		return paths, err
	}

	// 3. Client mTLS Certificate (CN="payments-svc", URI SAN="spiffe://cluster.local/ns/prod/sa/payments")
	cliKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return paths, err
	}
	spiffeURI, _ := url.Parse("spiffe://cluster.local/ns/prod/sa/payments")
	cliTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "payments-svc", Organization: []string{"Platform Mesh"}},
		NotBefore:    now,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{spiffeURI},
	}
	cliDER, err := x509.CreateCertificate(rand.Reader, cliTmpl, caTmpl, &cliKey.PublicKey, caKey)
	if err != nil {
		return paths, err
	}
	if err := writeKeyPair(paths.ClientCert, paths.ClientKey, cliDER, cliKey); err != nil {
		return paths, err
	}

	return paths, nil
}

func writeKeyPair(certPath, keyPath string, certDER []byte, key *ecdsa.PrivateKey) error {
	if err := writePEM(certPath, "CERTIFICATE", certDER, 0o644); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600)
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

// NewServerTLSConfig builds a hardened TLS 1.3 configuration with ALPN ("h2", "http/1.1")
// and client certificate verification against the provided CA.
func NewServerTLSConfig(paths CertPaths, clientAuth tls.ClientAuthType) (*tls.Config, error) {
	srvCert, err := tls.LoadX509KeyPair(paths.ServerCert, paths.ServerKey)
	if err != nil {
		return nil, fmt.Errorf("load server keypair: %w", err)
	}
	caPEM, err := os.ReadFile(paths.CACert)
	if err != nil {
		return nil, fmt.Errorf("read ca cert: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("failed to append CA certificate")
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{srvCert},
		ClientCAs:    caPool,
		ClientAuth:   clientAuth,
		NextProtos:   []string{"h2", "http/1.1"}, // ALPN negotiates HTTP/2 automatically over TLS
	}, nil
}

// ListenAndServeTLSGraceful runs a hardened HTTPS/mTLS server and shuts down on SIGINT/SIGTERM.
func ListenAndServeTLSGraceful(variant, addr string, handler http.Handler, tlsCfg *tls.Config) error {
	shutdownOtel, err := InitTelemetry(variant)
	if err != nil {
		return err
	}
	defer shutdownTelemetry(shutdownOtel)

	srv := NewHTTPServer(addr, WrapHandler(variant, handler))
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	srv.Protocols = &protocols
	srv.TLSConfig = tlsCfg

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting TLS server (TLS 1.3 + HTTP/2 ALPN + mTLS)", "variant", variant, "addr", addr)
		// Certs are already loaded in srv.TLSConfig.Certificates
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down TLS server gracefully", "variant", variant)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
