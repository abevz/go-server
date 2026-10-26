package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
)

func TestStdlibServer(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	shutdown, err := platform.InitTelemetry("stdlib-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(t.Context()) }()

	svc := domain.NewUserService()
	handler := platform.WrapHandler("stdlib-test", NewRouter(svc))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	t.Run("GET existing user returns 200 and X-Trace-Id", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/users/u-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if resp.Header.Get("X-Trace-Id") == "" {
			t.Errorf("expected X-Trace-Id header from OTel middleware")
		}

		var u domain.User
		if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if u.Name != "Aleksey" {
			t.Errorf("expected Aleksey, got %q", u.Name)
		}
	})

	t.Run("GET unknown user returns 404", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/users/u-999")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("POST user rejects unknown JSON fields with 400", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name":"Bob","email":"bob@example.com","hacker_field":true}`)
		resp, err := http.Post(ts.URL+"/users", "application/json", body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 on unknown field, got %d", resp.StatusCode)
		}
	})

	t.Run("POST valid user returns 201 and duplicate email returns 409", func(t *testing.T) {
		payload := `{"name":"Bob","email":"bob@example.com","role":"sre"}`
		resp, err := http.Post(ts.URL+"/users", "application/json", bytes.NewBufferString(payload))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}

		respDup, err := http.Post(ts.URL+"/users", "application/json", bytes.NewBufferString(payload))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		respDup.Body.Close()
		if respDup.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409 on duplicate email, got %d", respDup.StatusCode)
		}
	})
}
