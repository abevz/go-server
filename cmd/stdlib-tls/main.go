package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
)

const maxBodyBytes = 1 << 20 // 1 MB

type tlsHandler struct {
	svc *domain.UserService
}

func NewRouter(svc *domain.UserService) http.Handler {
	h := &tlsHandler{svc: svc}
	mux := http.NewServeMux()

	// Public HTTPS routes (TLS 1.3 + HTTP/2 negotiated via ALPN)
	mux.HandleFunc("GET /users", h.listUsers)
	mux.HandleFunc("GET /users/{id}", h.getUser)
	mux.HandleFunc("POST /users", h.createUser)

	// Zero-Trust mTLS route: requires verified X.509 Client Certificate (CN + SPIFFE URI SAN)
	mux.Handle("GET /mtls/whoami", requireClientCert(http.HandlerFunc(h.whoamiMTLS)))

	return mux
}

// requireClientCert checks that the TLS handshake verified a client certificate against ClientCAs.
func requireClientCert(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "mTLS client certificate required (pass --cert certs/client.crt --key certs/client.key)",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *tlsHandler) whoamiMTLS(w http.ResponseWriter, r *http.Request) {
	leaf := r.TLS.PeerCertificates[0]
	var spiffeID string
	if len(leaf.URIs) > 0 {
		spiffeID = leaf.URIs[0].String()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"client_cn":     leaf.Subject.CommonName,
		"spiffe_id":     spiffeID,
		"http_proto":    r.Proto, // "HTTP/2.0" negotiated automatically via TLS ALPN
		"tls_version":   tls.VersionName(r.TLS.Version),
		"cipher_suite":  tls.CipherSuiteName(r.TLS.CipherSuite),
	})
}

func (h *tlsHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *tlsHandler) getUser(w http.ResponseWriter, r *http.Request) {
	u, err := h.svc.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type createUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *tlsHandler) createUser(w http.ResponseWriter, r *http.Request) {
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

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

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
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

func main() {
	addr := ":8443"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	certDir := "./certs"
	if v := os.Getenv("CERT_DIR"); v != "" {
		certDir = v
	}

	paths, err := platform.EnsureDevCerts(certDir)
	if err != nil {
		slog.Error("failed to generate dev certs", "err", err)
		os.Exit(1)
	}
	slog.Info("TLS certificates ready", "ca", paths.CACert, "client_cert", paths.ClientCert)

	// VerifyClientCertIfGiven allows both standard HTTPS and mTLS routes on :8443.
	// Set MTLS_STRICT=1 to reject connections without a client cert during the TLS handshake itself.
	clientAuth := tls.VerifyClientCertIfGiven
	if os.Getenv("MTLS_STRICT") == "1" {
		clientAuth = tls.RequireAndVerifyClientCert
	}

	tlsCfg, err := platform.NewServerTLSConfig(paths, clientAuth)
	if err != nil {
		slog.Error("failed to configure TLS", "err", err)
		os.Exit(1)
	}

	svc := domain.NewUserService()
	if err := platform.ListenAndServeTLSGraceful("stdlib-tls", addr, NewRouter(svc), tlsCfg); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
