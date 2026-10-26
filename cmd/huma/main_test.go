package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
)

func TestHumaServer(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	shutdown, err := platform.InitTelemetry("huma-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(t.Context()) }()

	svc := domain.NewUserService()
	ts := httptest.NewServer(platform.WrapHandler("huma-test", NewRouter(svc)))
	defer ts.Close()

	t.Run("Auto-generated OpenAPI 3.1 spec is served at /openapi.json", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/openapi.json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("Invalid input fails schema validation with 422 Problem Details", func(t *testing.T) {
		// Invalid role enum + short name
		payload := `{"name":"A","email":"not-an-email","role":"ceo"}`
		resp, err := http.Post(ts.URL+"/users", "application/json", bytes.NewBufferString(payload))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 Unprocessable Entity from Huma validator, got %d", resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("Content-Type"), "application/problem+json") {
			t.Errorf("expected RFC 9457 application/problem+json, got %q", resp.Header.Get("Content-Type"))
		}
	})

	t.Run("Valid POST creates user with 201", func(t *testing.T) {
		payload := `{"name":"Alice","email":"alice@example.com","role":"sre"}`
		resp, err := http.Post(ts.URL+"/users", "application/json", bytes.NewBufferString(payload))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
		}
	})
}
