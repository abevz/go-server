package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
)

func TestStdlibTLSAndMTLS(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	shutdown, err := platform.InitTelemetry("stdlib-tls-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(t.Context()) }()

	certDir := t.TempDir()
	paths, err := platform.EnsureDevCerts(certDir)
	if err != nil {
		t.Fatalf("EnsureDevCerts failed: %v", err)
	}

	srvTLS, err := platform.NewServerTLSConfig(paths, tls.VerifyClientCertIfGiven)
	if err != nil {
		t.Fatalf("NewServerTLSConfig failed: %v", err)
	}

	svc := domain.NewUserService()
	ts := httptest.NewUnstartedServer(platform.WrapHandler("stdlib-tls-test", NewRouter(svc)))
	ts.EnableHTTP2 = true
	ts.TLS = srvTLS
	ts.StartTLS()
	defer ts.Close()

	caPEM, err := os.ReadFile(paths.CACert)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	// Client 1: Trusts CA, no client certificate (normal HTTPS client)
	httpsClient := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{
				RootCAs:    caPool,
				MinVersion: tls.VersionTLS13,
			},
		},
	}

	t.Run("HTTPS GET /users/u-1 negotiates TLS 1.3 + HTTP/2 via ALPN", func(t *testing.T) {
		resp, err := httpsClient.Get(ts.URL + "/users/u-1")
		if err != nil {
			t.Fatalf("HTTPS request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if resp.Proto != "HTTP/2.0" {
			t.Errorf("expected HTTP/2.0 via ALPN, got %s", resp.Proto)
		}
		if resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
			t.Errorf("expected TLS 1.3")
		}
	})

	t.Run("HTTPS POST creates a readable user and rejects duplicate email", func(t *testing.T) {
		payload := `{"name":"Bob","email":"bob@example.com","role":"sre"}`
		resp, err := httpsClient.Post(ts.URL+"/users", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		if resp.Proto != "HTTP/2.0" || resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
			t.Fatalf("expected POST over HTTP/2 and TLS 1.3, got %s", resp.Proto)
		}
		var created domain.User
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatal(err)
		}
		if created.ID == "" || created.Name != "Bob" || created.Email != "bob@example.com" || created.Role != "sre" || created.CreatedAt.IsZero() {
			t.Fatalf("unexpected created user: %+v", created)
		}
		fetched, err := httpsClient.Get(ts.URL + "/users/" + created.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer fetched.Body.Close()
		var user domain.User
		if err := json.NewDecoder(fetched.Body).Decode(&user); err != nil {
			t.Fatal(err)
		}
		if fetched.StatusCode != http.StatusOK || user != created {
			t.Fatalf("created user was not persisted: status=%d user=%+v", fetched.StatusCode, user)
		}
		duplicate, err := httpsClient.Post(ts.URL+"/users", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		defer duplicate.Body.Close()
		if duplicate.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409 for duplicate email, got %d", duplicate.StatusCode)
		}
	})

	t.Run("HTTPS POST rejects invalid requests without creating users", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			body   string
			status int
		}{
			{"malformed JSON", `{"name":`, http.StatusBadRequest},
			{"unknown field", `{"name":"Alice","email":"alice@example.com","extra":true}`, http.StatusBadRequest},
			{"invalid user", `{"name":"A","email":"invalid"}`, http.StatusBadRequest},
			{"oversized body", `{"name":"` + strings.Repeat("x", maxBodyBytes) + `"}`, http.StatusRequestEntityTooLarge},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before, err := svc.ListUsers(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				resp, err := httpsClient.Post(ts.URL+"/users", "application/json", bytes.NewBufferString(tc.body))
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != tc.status {
					t.Fatalf("expected %d, got %d", tc.status, resp.StatusCode)
				}
				var body struct{ Error string }
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Error == "" {
					t.Fatalf("expected JSON error: %+v (%v)", body, err)
				}
				after, err := svc.ListUsers(t.Context())
				if err != nil || len(after) != len(before) {
					t.Fatalf("rejected request changed users: %d -> %d, err=%v", len(before), len(after), err)
				}
			})
		}
	})

	t.Run("GET /mtls/whoami without client cert returns 401", func(t *testing.T) {
		resp, err := httpsClient.Get(ts.URL + "/mtls/whoami")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 without client cert, got %d", resp.StatusCode)
		}
	})

	t.Run("GET /mtls/whoami with client cert verifies CN and SPIFFE URI SAN", func(t *testing.T) {
		cliCert, err := tls.LoadX509KeyPair(paths.ClientCert, paths.ClientKey)
		if err != nil {
			t.Fatalf("load client cert: %v", err)
		}
		mtlsClient := &http.Client{
			Transport: &http.Transport{
				ForceAttemptHTTP2: true,
				TLSClientConfig: &tls.Config{
					RootCAs:      caPool,
					Certificates: []tls.Certificate{cliCert},
					MinVersion:   tls.VersionTLS13,
				},
			},
		}

		resp, err := mtlsClient.Get(ts.URL + "/mtls/whoami")
		if err != nil {
			t.Fatalf("mTLS request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK with client cert, got %d", resp.StatusCode)
		}

		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if body["client_cn"] != "payments-svc" {
			t.Errorf("expected client_cn=payments-svc, got %v", body["client_cn"])
		}
		if body["spiffe_id"] != "spiffe://cluster.local/ns/prod/sa/payments" {
			t.Errorf("unexpected spiffe_id: %v", body["spiffe_id"])
		}
		if body["http_proto"] != "HTTP/2.0" {
			t.Errorf("expected HTTP/2.0, got %v", body["http_proto"])
		}
	})
}
