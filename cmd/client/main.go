package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/abevz/go-server/gen/user/v1"
	"github.com/abevz/go-server/gen/user/v1/userv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type options struct {
	protocol, baseURL, operation string
	id, name, email, role        string
	caFile, certFile, keyFile    string
	timeout                      time.Duration
}

func main() { os.Exit(execute()) }

func execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var cfg options
	flags := flag.NewFlagSet("client", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.protocol, "protocol", "rest", "API protocol: rest, connect (Protobuf), connect-json, grpc")
	flags.StringVar(&cfg.baseURL, "url", "", "Server base URL (default http://localhost:8081 for REST, :8084 for RPC)")
	flags.StringVar(&cfg.operation, "op", "get", "Operation: get, list, create; REST also supports whoami")
	flags.StringVar(&cfg.id, "id", "u-1", "User ID for get")
	flags.StringVar(&cfg.name, "name", "", "User name for create")
	flags.StringVar(&cfg.email, "email", "", "Email for create")
	flags.StringVar(&cfg.role, "role", "", "Optional role for create")
	flags.StringVar(&cfg.caFile, "ca", "", "PEM CA bundle for verifying the HTTPS server")
	flags.StringVar(&cfg.certFile, "cert", "", "PEM client certificate for mTLS (requires -key)")
	flags.StringVar(&cfg.keyFile, "key", "", "PEM client private key for mTLS (requires -cert)")
	flags.DurationVar(&cfg.timeout, "timeout", 10*time.Second, "Request timeout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "client: unexpected positional arguments")
		return 2
	}
	if err := cfg.validate(); err != nil {
		fmt.Fprintln(stderr, "client:", err)
		return 2
	}
	httpClient, err := cfg.httpClient()
	if err != nil {
		fmt.Fprintln(stderr, "client:", err)
		return 1
	}
	defer httpClient.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	if cfg.protocol == "rest" {
		err = cfg.callREST(ctx, httpClient, stdout)
	} else {
		err = cfg.callRPC(ctx, httpClient, stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "client:", err)
		return 1
	}
	return 0
}

func (cfg *options) validate() error {
	switch cfg.protocol {
	case "rest", "connect", "connect-json", "grpc":
	default:
		return fmt.Errorf("unknown protocol %q", cfg.protocol)
	}
	if cfg.baseURL == "" {
		cfg.baseURL = "http://localhost:8084"
		if cfg.protocol == "rest" {
			cfg.baseURL = "http://localhost:8081"
		}
	}
	u, err := url.Parse(cfg.baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("-url must be an http:// or https:// base URL without credentials, query, or fragment")
	}
	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")
	if (cfg.certFile == "") != (cfg.keyFile == "") {
		return errors.New("-cert and -key must be provided together")
	}
	if u.Scheme != "https" && (cfg.caFile != "" || cfg.certFile != "") {
		return errors.New("TLS certificate options require an https:// URL")
	}
	if cfg.timeout <= 0 {
		return errors.New("-timeout must be positive")
	}
	switch cfg.operation {
	case "get":
		if strings.TrimSpace(cfg.id) == "" {
			return errors.New("get requires a non-empty -id")
		}
	case "list":
	case "create":
		if strings.TrimSpace(cfg.name) == "" || strings.TrimSpace(cfg.email) == "" {
			return errors.New("create requires -name and -email")
		}
	case "whoami":
		if cfg.protocol != "rest" {
			return errors.New("whoami is only available with -protocol rest")
		}
	default:
		return fmt.Errorf("unknown operation %q", cfg.operation)
	}
	return nil
}

func (cfg options) httpClient() (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if cfg.caFile != "" {
		pem, err := os.ReadFile(cfg.caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA bundle contains no valid certificates")
		}
		tlsConfig.RootCAs = pool
	}
	if cfg.certFile != "" {
		pair, err := tls.LoadX509KeyPair(cfg.certFile, cfg.keyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	var protocols http.Protocols
	protocols.SetHTTP2(true)
	protocols.SetHTTP1(cfg.protocol != "grpc")
	// gRPC requires HTTP/2; native h2c supports the lab's cleartext RPC server.
	protocols.SetUnencryptedHTTP2(cfg.protocol == "grpc" && strings.HasPrefix(cfg.baseURL, "http://"))
	transport.Protocols = &protocols
	return &http.Client{Transport: transport}, nil
}

func (cfg options) callREST(ctx context.Context, client *http.Client, stdout io.Writer) error {
	method, path := http.MethodGet, "/users"
	var body io.Reader
	switch cfg.operation {
	case "get":
		path += "/" + url.PathEscape(cfg.id)
	case "whoami":
		path = "/mtls/whoami"
	case "create":
		payload, err := json.Marshal(struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Role  string `json:"role,omitempty"`
		}{cfg.name, cfg.email, cfg.role})
		if err != nil {
			return err
		}
		method, body = http.MethodPost, bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfg.baseURL+path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	const maxResponseBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseBytes {
		return errors.New("response exceeds 4 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(json.RawMessage(data))
}

func (cfg options) callRPC(ctx context.Context, httpClient *http.Client, stdout io.Writer) error {
	var opts []connect.ClientOption
	switch cfg.protocol {
	case "grpc":
		opts = append(opts, connect.WithGRPC())
	case "connect-json":
		opts = append(opts, connect.WithProtoJSON())
	}
	client := userv1connect.NewUserServiceClient(httpClient, cfg.baseURL, opts...)
	var message proto.Message
	switch cfg.operation {
	case "get":
		resp, err := client.GetUser(ctx, connect.NewRequest(&userv1.GetUserRequest{Id: cfg.id}))
		if err != nil {
			return err
		}
		message = resp.Msg
	case "list":
		resp, err := client.ListUsers(ctx, connect.NewRequest(&userv1.ListUsersRequest{}))
		if err != nil {
			return err
		}
		message = resp.Msg
	case "create":
		resp, err := client.CreateUser(ctx, connect.NewRequest(&userv1.CreateUserRequest{Name: cfg.name, Email: cfg.email, Role: cfg.role}))
		if err != nil {
			return err
		}
		message = resp.Msg
	}
	data, err := (protojson.MarshalOptions{Indent: "  "}).Marshal(message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(data))
	return err
}
