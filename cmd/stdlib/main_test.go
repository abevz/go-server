package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
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

	t.Run("POST user rejects trailing garbage and multiple JSON objects with 400", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			body string
		}{
			{"trailing garbage", `{"name":"Ann","email":"a@b.c"}garbage`},
			{"two JSON objects", `{"name":"Ann","email":"a@b.c"}{"name":"Bob","email":"b@b.c"}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before, err := svc.ListUsers(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				resp, err := http.Post(ts.URL+"/users", "application/json", bytes.NewBufferString(tc.body))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusBadRequest {
					t.Fatalf("expected 400, got %d", resp.StatusCode)
				}
				var errResp struct {
					Error string `json:"error"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil || errResp.Error == "" {
					t.Fatalf("expected non-empty JSON error, got %+v (%v)", errResp, err)
				}
				after, err := svc.ListUsers(t.Context())
				if err != nil || len(after) != len(before) {
					t.Fatalf("rejected request modified users: %d -> %d, err=%v", len(before), len(after), err)
				}
			})
		}
	})
}

func TestStdlibListUsersDeterministicOrder(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	shutdown, err := platform.InitTelemetry("stdlib-order-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(t.Context()) }()

	svc := domain.NewUserService()
	for i := 2; i <= 12; i++ {
		if _, err := svc.CreateUser(t.Context(), fmt.Sprintf("User %d", i), fmt.Sprintf("u%d@example.com", i), "developer"); err != nil {
			t.Fatal(err)
		}
	}

	ts := httptest.NewServer(platform.WrapHandler("stdlib-order-test", NewRouter(svc)))
	defer ts.Close()

	fetchIDs := func() []string {
		resp, err := http.Get(ts.URL + "/users")
		if err != nil {
			t.Fatalf("GET /users failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var users []domain.User
		if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		ids := make([]string, len(users))
		for i, u := range users {
			ids[i] = u.ID
		}
		return ids
	}

	first := fetchIDs()
	second := fetchIDs()
	want := []string{"u-1", "u-2", "u-3", "u-4", "u-5", "u-6", "u-7", "u-8", "u-9", "u-10", "u-11", "u-12"}
	if !slices.Equal(first, want) {
		t.Fatalf("first GET /users order = %v, want %v", first, want)
	}
	if !slices.Equal(second, first) {
		t.Fatalf("second GET /users order = %v, want %v", second, first)
	}
}
