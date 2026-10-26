package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
)

const maxBodyBytes = 1 << 20 // 1 MB

type stdlibHandler struct {
	svc *domain.UserService
}

func NewRouter(svc *domain.UserService) http.Handler {
	h := &stdlibHandler{svc: svc}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "variant": "stdlib"})
	})
	mux.HandleFunc("GET /users", h.listUsers)
	mux.HandleFunc("GET /users/{id}", h.getUser)
	mux.HandleFunc("POST /users", h.createUser)

	return mux
}

func (h *stdlibHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *stdlibHandler) getUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

type createUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *stdlibHandler) createUser(w http.ResponseWriter, r *http.Request) {
	// Hardening: prevent OOM via MaxBytesReader and reject unknown JSON fields.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req createUserRequest
	if err := dec.Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	user, err := h.svc.CreateUser(r.Context(), req.Name, req.Email, req.Role)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// Helper 1: writes JSON response with status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode json", "err", err)
	}
}

// Helper 2: maps domain errors to HTTP status codes and calls writeJSON.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	msg := "internal server error"

	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
		msg = err.Error()
	case errors.Is(err, domain.ErrInvalidInput):
		status = http.StatusBadRequest
		msg = err.Error()
	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
		msg = err.Error()
	default:
		slog.Error("unhandled error", "err", err)
	}

	writeJSON(w, status, map[string]string{"error": msg})
}

func main() {
	addr := ":8081"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	svc := domain.NewUserService()
	if err := platform.ListenAndServeGraceful("stdlib", addr, NewRouter(svc)); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
