package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	userv1 "github.com/abevz/go-server/gen/user/v1"
	"github.com/abevz/go-server/gen/user/v1/userv1connect"
	"github.com/abevz/go-server/internal/platform"
)

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRESTOperations(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "u-1" {
			t.Errorf("unexpected ID: %s", r.PathValue("id"))
		}
		_, _ = w.Write([]byte(`{"id":"u-1","name":"Aleksey"}`))
	})
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"u-1","name":"Aleksey"}]`))
	})
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] != "Bob" || body["email"] != "bob@example.com" || body["role"] != "sre" {
			t.Errorf("incorrect create request: %v", body)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"u-2","name":"Bob"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	for _, op := range []string{"get", "list", "create"} {
		t.Run(op, func(t *testing.T) {
			args := []string{"-url", ts.URL, "-op", op}
			if op == "create" {
				args = append(args, "-name", "Bob", "-email", "bob@example.com", "-role", "sre")
			}
			code, out, err := invoke(t, args...)
			if code != 0 || err != "" || !json.Valid([]byte(out)) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, err)
			}
		})
	}
}

func TestStrictMTLS(t *testing.T) {
	paths, err := platform.EnsureDevCerts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := platform.NewServerTLSConfig(paths, tls.RequireAndVerifyClientCert)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Proto != "HTTP/2.0" || r.TLS == nil || r.TLS.Version != tls.VersionTLS13 || len(r.TLS.VerifiedChains) == 0 {
			t.Error("expected HTTP/2, TLS 1.3, and a verified client certificate")
		}
		if r.URL.Path == "/mtls/whoami" {
			_ = json.NewEncoder(w).Encode(map[string]string{"client_cn": r.TLS.PeerCertificates[0].Subject.CommonName})
		} else {
			_, _ = w.Write([]byte(`{"id":"u-1"}`))
		}
	}))
	ts.TLS = tlsConfig
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	base := []string{"-url", ts.URL, "-ca", paths.CACert}
	if code, out, _ := invoke(t, base...); code != 1 || out != "" {
		t.Fatalf("missing certificate must fail the handshake: code=%d out=%q", code, out)
	}
	base = append(base, "-cert", paths.ClientCert, "-key", paths.ClientKey)
	for _, op := range []string{"get", "whoami"} {
		code, out, err := invoke(t, append(append([]string{}, base...), "-op", op)...)
		if code != 0 || err != "" || !json.Valid([]byte(out)) {
			t.Fatalf("mTLS %s: code=%d out=%q err=%q", op, code, out, err)
		}
		if op == "whoami" && !strings.Contains(out, "payments-svc") {
			t.Fatalf("expected certificate identity, got %s", out)
		}
	}
	wrongCA, err := platform.EnsureDevCerts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := invoke(t, "-url", ts.URL, "-ca", wrongCA.CACert, "-cert", paths.ClientCert, "-key", paths.ClientKey)
	if code != 1 || out != "" {
		t.Fatalf("untrusted server must fail verification: code=%d out=%q", code, out)
	}
}

type rpcFixture struct {
	userv1connect.UnimplementedUserServiceHandler
}

func (*rpcFixture) GetUser(_ context.Context, req *connect.Request[userv1.GetUserRequest]) (*connect.Response[userv1.GetUserResponse], error) {
	if req.Msg.GetId() == "missing" {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return connect.NewResponse(&userv1.GetUserResponse{User: &userv1.User{Id: req.Msg.GetId(), Name: "Aleksey"}}), nil
}

func (*rpcFixture) ListUsers(context.Context, *connect.Request[userv1.ListUsersRequest]) (*connect.Response[userv1.ListUsersResponse], error) {
	return connect.NewResponse(&userv1.ListUsersResponse{Users: []*userv1.User{{Id: "u-1", Name: "Aleksey"}}}), nil
}

func (*rpcFixture) CreateUser(_ context.Context, req *connect.Request[userv1.CreateUserRequest]) (*connect.Response[userv1.CreateUserResponse], error) {
	return connect.NewResponse(&userv1.CreateUserResponse{User: &userv1.User{Id: "u-2", Name: req.Msg.GetName(), Email: req.Msg.GetEmail(), Role: req.Msg.GetRole()}}), nil
}

func TestRPCProtocols(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		mtls     bool
	}{
		{protocol: "connect"}, {protocol: "connect-json"}, {protocol: "grpc"},
		{protocol: "grpc", mtls: true},
	} {
		name := tc.protocol
		if tc.mtls {
			name += " with strict mTLS"
		}
		t.Run(name, func(t *testing.T) {
			protocol := tc.protocol
			path, handler := userv1connect.NewUserServiceHandler(&rpcFixture{})
			mux := http.NewServeMux()
			mux.Handle(path, handler)
			observed := make(chan string, 8)
			wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observed <- r.Proto + " " + r.Header.Get("Content-Type")
				mux.ServeHTTP(w, r)
			})
			ts := httptest.NewUnstartedServer(wrapped)
			ts.Config = platform.NewHTTPServer("", wrapped)
			var tlsArgs []string
			if tc.mtls {
				paths, err := platform.EnsureDevCerts(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				ts.TLS, err = platform.NewServerTLSConfig(paths, tls.RequireAndVerifyClientCert)
				if err != nil {
					t.Fatal(err)
				}
				ts.EnableHTTP2 = true
				ts.StartTLS()
				tlsArgs = []string{"-ca", paths.CACert, "-cert", paths.ClientCert, "-key", paths.ClientKey}
			} else {
				ts.Start()
			}
			defer ts.Close()
			for _, op := range []string{"get", "list", "create"} {
				args := []string{"-protocol", protocol, "-url", ts.URL, "-op", op}
				args = append(args, tlsArgs...)
				if op == "create" {
					args = append(args, "-name", "Bob", "-email", "bob@example.com", "-role", "sre")
				}
				code, out, err := invoke(t, args...)
				if code != 0 || err != "" || !json.Valid([]byte(out)) {
					t.Fatalf("%s: code=%d out=%q err=%q", op, code, out, err)
				}
				want := "HTTP/1.1 application/proto"
				if protocol == "grpc" {
					want = "HTTP/2.0 application/grpc"
				} else if protocol == "connect-json" {
					want = "HTTP/1.1 application/json"
				}
				if got := <-observed; !strings.HasPrefix(got, want) {
					t.Fatalf("expected %s, got %s", want, got)
				}
				var output map[string]json.RawMessage
				if err := json.Unmarshal([]byte(out), &output); err != nil {
					t.Fatal(err)
				}
				key := "user"
				if op == "list" {
					key = "users"
				}
				if len(output[key]) == 0 {
					t.Fatalf("missing %s in RPC response: %s", key, out)
				}
				if op == "list" {
					var users []map[string]string
					if err := json.Unmarshal(output[key], &users); err != nil || len(users) != 1 || users[0]["id"] != "u-1" {
						t.Fatalf("incorrect user list: %s (%v)", out, err)
					}
				} else {
					var user map[string]string
					if err := json.Unmarshal(output[key], &user); err != nil {
						t.Fatal(err)
					}
					if op == "get" && (user["id"] != "u-1" || user["name"] != "Aleksey") {
						t.Fatalf("incorrect fetched user: %s", out)
					}
					if op == "create" && (user["name"] != "Bob" || user["email"] != "bob@example.com" || user["role"] != "sre") {
						t.Fatalf("incorrect create payload or response: %s", out)
					}
				}
			}
			args := append([]string{"-protocol", protocol, "-url", ts.URL, "-id", "missing"}, tlsArgs...)
			code, out, err := invoke(t, args...)
			if code != 1 || out != "" || !strings.Contains(err, "not_found") {
				t.Fatalf("RPC error handling: code=%d out=%q err=%q", code, out, err)
			}
		})
	}
}

func TestUsageAndRuntimeErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-protocol", "invalid"}, {"-url", "ftp://localhost"}, {"-op", "invalid"},
		{"-op", "get", "-id", ""}, {"-op", "create"}, {"-timeout", "0"},
		{"-cert", "client.crt"}, {"-ca", "ca.crt"}, {"unexpected"},
		{"-protocol", "grpc", "-op", "whoami"},
	} {
		if code, out, err := invoke(t, args...); code != 2 || out != "" || err == "" {
			t.Fatalf("args=%q code=%d out=%q err=%q", args, code, out, err)
		}
	}
	if code, out, help := invoke(t, "-help"); code != 0 || out != "" || !strings.Contains(help, "-protocol") {
		t.Fatalf("help: code=%d out=%q help=%q", code, out, help)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "test failure", http.StatusInternalServerError)
	}))
	defer ts.Close()
	if code, out, err := invoke(t, "-url", ts.URL); code != 1 || out != "" || !strings.Contains(err, "HTTP 500") {
		t.Fatalf("HTTP error: code=%d out=%q err=%q", code, out, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out, err bytes.Buffer
	if code := run(ctx, []string{"-url", ts.URL}, &out, &err); code != 1 || out.Len() != 0 {
		t.Fatalf("canceled request: code=%d out=%q err=%q", code, out.String(), err.String())
	}
	pem := t.TempDir() + "/invalid-ca.pem"
	if err := os.WriteFile(pem, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, err := invoke(t, "-url", "https://localhost:8443", "-ca", pem); code != 1 || out != "" || err == "" {
		t.Fatalf("invalid CA: code=%d out=%q err=%q", code, out, err)
	}
}
