package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
)

func TestOpenAPIServer(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	shutdown, err := platform.InitTelemetry("openapi-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(t.Context()) }()

	svc := domain.NewUserService()
	ts := httptest.NewServer(platform.WrapHandler("openapi-test", NewRouter(svc)))
	defer ts.Close()

	t.Run("GET /users/u-1 returns 200", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/users/u-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("GET /users/unknown returns typed 404", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/users/unknown")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("POST /users rejects invalid schema before reaching service", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/users", "application/json", bytes.NewBufferString(`{"name":"A","email":"bad"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
		}
	})
}

func TestOpenAPIRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		contentType string
		status      int
	}{
		{name: "missing name", body: `{"email":"bob@example.com"}`},
		{name: "null name", body: `{"name":null,"email":"bob@example.com"}`},
		{name: "invalid email", body: `{"name":"Bob","email":"@"}`},
		{name: "unknown field", body: `{"name":"Bob","email":"bob@example.com","extra":true}`},
		{name: "trailing JSON", body: `{"name":"Bob","email":"bob@example.com"} {}`},
		{name: "malformed JSON", body: `{"name":"Bob",`},
		{name: "empty body"},
		{name: "null body", body: `null`},
		{name: "wrong content type", body: `{"name":"Bob","email":"bob@example.com"}`, contentType: "text/plain"},
		{name: "oversized body", body: `{"name":"` + strings.Repeat("x", 1<<20) + `","email":"bob@example.com"}`, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := domain.NewUserService()
			ts := httptest.NewServer(NewRouter(svc))
			defer ts.Close()
			contentType := tc.contentType
			if contentType == "" {
				contentType = "application/json"
			}
			resp, err := http.Post(ts.URL+"/users", contentType, bytes.NewBufferString(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			status := tc.status
			if status == 0 {
				status = http.StatusBadRequest
			}
			if resp.StatusCode != status {
				t.Fatalf("expected %d, got %d", status, resp.StatusCode)
			}
			if resp.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("expected JSON error, got %s", resp.Header.Get("Content-Type"))
			}
			var body struct{ Error string }
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Error == "" {
				t.Fatalf("expected JSON error, got %+v (%v)", body, err)
			}
			users, err := svc.ListUsers(t.Context())
			if err != nil || len(users) != 1 {
				t.Fatalf("rejected request changed state: users=%d, err=%v", len(users), err)
			}
		})
	}
}

func TestOpenAPICreateAndListUsers(t *testing.T) {
	ts := httptest.NewServer(NewRouter(domain.NewUserService()))
	defer ts.Close()
	payload := `{"name":"Bob","email":"bob@example.com"}`
	resp, err := http.Post(ts.URL+"/users", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created domain.User
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "u-2" || created.Name != "Bob" || created.Email != "bob@example.com" || created.Role != "developer" || created.CreatedAt.IsZero() {
		t.Fatalf("incorrect schema mapping or role default: %+v", created)
	}
	duplicate, err := http.Post(ts.URL+"/users", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", duplicate.StatusCode)
	}
	listed, err := http.Get(ts.URL + "/users")
	if err != nil {
		t.Fatal(err)
	}
	defer listed.Body.Close()
	var users []domain.User
	if err := json.NewDecoder(listed.Body).Decode(&users); err != nil {
		t.Fatal(err)
	}
	if listed.StatusCode != http.StatusOK || len(users) != 2 {
		t.Fatalf("expected list with two users, got status=%d users=%+v", listed.StatusCode, users)
	}
}
