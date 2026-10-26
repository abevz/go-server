package platform

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

// statusRecorder is the "7-ish lines in one spot" wrapper around http.ResponseWriter
// to capture the HTTP status code for structured logs and metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap allows standard library http.ResponseController (Go 1.20+) to access
// underlying Flusher/Hijacker/SetWriteDeadline interfaces through the wrapper.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// WrapHandler applies:
// 1. OpenTelemetry HTTP instrumentation (otelhttp)
// 2. Structured access logging (log/slog) with statusRecorder, r.Pattern (Go 1.23+), and trace_id
// 3. Go 1.25+ stdlib CrossOriginProtection (optional CSRF guard for state-changing requests)
func WrapHandler(serviceName string, mux http.Handler) http.Handler {
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// Extract trace_id from the OTel span in r.Context() and return it in X-Trace-Id header for easy debugging.
		var traceID string
		if spanCtx := trace.SpanFromContext(r.Context()).SpanContext(); spanCtx.HasTraceID() {
			traceID = spanCtx.TraceID().String()
			rec.Header().Set("X-Trace-Id", traceID)
		}

		mux.ServeHTTP(rec, r)

		// r.Pattern is populated by Go 1.23+ ServeMux after matching (e.g., "GET /users/{id}")
		// preventing high-cardinality label explosion in metrics/logs.
		pattern := r.Pattern
		if pattern == "" {
			pattern = r.URL.Path
		}

		slog.InfoContext(r.Context(), "http_request",
			"service", serviceName,
			"method", r.Method,
			"path", r.URL.Path,
			"pattern", pattern,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"trace_id", traceID,
		)
	})

	// Go 1.25+ stdlib CSRF protection (blocks cross-origin POST/PUT/DELETE from browsers
	// when Sec-Fetch-Site or Origin mismatches, while allowing non-browser CLI/curl/service calls).
	cop := http.NewCrossOriginProtection()
	protected := cop.Handler(logged)

	return otelhttp.NewHandler(protected, serviceName)
}

// NewHTTPServer creates a hardened stdlib http.Server with explicit timeouts
// and native Go 1.24+ h2c (Unencrypted HTTP/2) enabled alongside HTTP/1.1,
// so plaintext gRPC/grpcurl and normal HTTP/1.1 curl work on the same port.
func NewHTTPServer(addr string, handler http.Handler) *http.Server {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true) // Native h2c in stdlib! Needed for `grpcurl -plaintext`

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		Protocols:         &protocols,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}
}

// ListenAndServeGraceful runs the HTTP server and shuts it down cleanly on SIGINT/SIGTERM.
func ListenAndServeGraceful(variant string, addr string, handler http.Handler) error {
	shutdownOtel, err := InitTelemetry(variant)
	if err != nil {
		return err
	}
	defer shutdownTelemetry(shutdownOtel)

	srv := NewHTTPServer(addr, WrapHandler(variant, handler))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting server", "variant", variant, "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down server gracefully", "variant", variant)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
