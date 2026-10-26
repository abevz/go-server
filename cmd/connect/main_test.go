package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	userv1 "github.com/abevz/go-server/gen/user/v1"
	"github.com/abevz/go-server/gen/user/v1/userv1connect"
	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
)

func TestConnectProtocols(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []connect.ClientOption
		grpc bool
	}{
		{name: "Connect Protobuf"},
		{name: "Connect JSON", opts: []connect.ClientOption{connect.WithProtoJSON()}},
		{name: "gRPC over h2c", opts: []connect.ClientOption{connect.WithGRPC()}, grpc: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := domain.NewUserService()
			protocol := make(chan string, 1)
			router := NewRouter(svc)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case protocol <- r.Proto:
				default:
				}
				router.ServeHTTP(w, r)
			})
			// Exercise the production protocol configuration, including native h2c.
			ts := httptest.NewUnstartedServer(handler)
			ts.Config = platform.NewHTTPServer("", handler)
			ts.Start()
			defer ts.Close()
			clientHTTP := ts.Client()
			if tc.grpc {
				var protocols http.Protocols
				protocols.SetUnencryptedHTTP2(true)
				transport := &http.Transport{Protocols: &protocols}
				defer transport.CloseIdleConnections()
				clientHTTP = &http.Client{Transport: transport}
			}
			client := userv1connect.NewUserServiceClient(clientHTTP, ts.URL, tc.opts...)

			res, err := client.GetUser(t.Context(), connect.NewRequest(&userv1.GetUserRequest{Id: "u-1"}))
			if err != nil {
				t.Fatal(err)
			}
			if res.Msg.GetUser().GetName() != "Aleksey" || res.Msg.GetUser().GetCreatedAt() == "" {
				t.Fatalf("unexpected user: %v", res.Msg)
			}
			if got := <-protocol; tc.grpc && got != "HTTP/2.0" {
				t.Fatalf("gRPC must use HTTP/2, got %s", got)
			}

			created, err := client.CreateUser(t.Context(), connect.NewRequest(&userv1.CreateUserRequest{
				Name: "Bob", Email: "bob@example.com", Role: "sre",
			}))
			if err != nil {
				t.Fatal(err)
			}
			if created.Msg.GetUser().GetId() != "u-2" || created.Msg.GetUser().GetRole() != "sre" {
				t.Fatalf("unexpected created user: %v", created.Msg)
			}
			_, err = client.CreateUser(t.Context(), connect.NewRequest(&userv1.CreateUserRequest{
				Name: "Bob", Email: "bob@example.com",
			}))
			if connect.CodeOf(err) != connect.CodeAlreadyExists {
				t.Fatalf("expected AlreadyExists, got %v", err)
			}
			listed, err := client.ListUsers(t.Context(), connect.NewRequest(&userv1.ListUsersRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			if len(listed.Msg.GetUsers()) != 2 {
				t.Fatalf("expected two users, got %v", listed.Msg)
			}
			for _, id := range []string{"", "   ", "u-404"} {
				_, err := client.GetUser(t.Context(), connect.NewRequest(&userv1.GetUserRequest{Id: id}))
				want := connect.CodeInvalidArgument
				if id == "u-404" {
					want = connect.CodeNotFound
				}
				if connect.CodeOf(err) != want {
					t.Fatalf("id %q: expected %v, got %v", id, want, err)
				}
			}
			_, err = client.CreateUser(t.Context(), connect.NewRequest(&userv1.CreateUserRequest{Name: "A", Email: "invalid"}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("expected InvalidArgument, got %v", err)
			}
		})
	}
}

func TestConnectPlainJSON(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	shutdown, err := platform.InitTelemetry("connect-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(t.Context()) }()
	ts := httptest.NewServer(platform.WrapHandler("connect-test", NewRouter(domain.NewUserService())))
	defer ts.Close()
	resp, err := http.Post(ts.URL+userv1connect.UserServiceGetUserProcedure, "application/json", bytes.NewBufferString(`{"id":"u-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Trace-Id") == "" {
		t.Fatalf("unexpected response: status=%d, headers=%v", resp.StatusCode, resp.Header)
	}
	var body struct {
		User struct {
			Name      string `json:"name"`
			CreatedAt string `json:"createdAt"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.User.Name != "Aleksey" || body.User.CreatedAt == "" {
		t.Fatalf("unexpected ProtoJSON: %+v", body)
	}
}

func TestConnectRejectsMalformedProtobuf(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body []byte
	}{
		{"truncated field", userv1connect.UserServiceGetUserProcedure, []byte{0x0a, 0x05, 'u'}},
		{"valid ID followed by malformed tag", userv1connect.UserServiceGetUserProcedure, []byte{0x0a, 0x03, 'u', '-', '1', 0xff}},
		{"invalid UTF-8", userv1connect.UserServiceGetUserProcedure, []byte{0x0a, 0x01, 0xff}},
		{"malformed create must not mutate state", userv1connect.UserServiceCreateUserProcedure,
			append(append([]byte{0x0a, 0x03, 'B', 'o', 'b', 0x12, 0x0f}, []byte("bob@example.com")...), 0xff)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := domain.NewUserService()
			ts := httptest.NewServer(NewRouter(svc))
			defer ts.Close()
			resp, err := http.Post(ts.URL+tc.path, "application/proto", bytes.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("malformed Protobuf: expected 400, got %d", resp.StatusCode)
			}
			users, err := svc.ListUsers(t.Context())
			if err != nil || len(users) != 1 {
				t.Fatalf("rejected request changed state: users=%d, err=%v", len(users), err)
			}
		})
	}
}
