package platform

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func preserveTelemetryGlobals(t *testing.T) {
	t.Helper()
	provider := otel.GetTracerProvider()
	propagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(provider)
		otel.SetTextMapPropagator(propagator)
	})
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
}

func TestTelemetryExportsHTTPAndChildSpansOverOTLP(t *testing.T) {
	preserveTelemetryGlobals(t)
	received := make(chan *collectortrace.ExportTraceServiceRequest, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("unexpected OTLP request: %s %s, type=%s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var export collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &export); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- &export
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK) // An empty protobuf ExportTraceServiceResponse.
	}))
	defer receiver.Close()
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "60000") // Exercise flushing on shutdown.
	t.Setenv("OTEL_SERVICE_NAME", "configured-service")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=lab")
	shutdown, err := InitTelemetry("fallback-service")
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownTelemetry(shutdown)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, span := otel.Tracer("go-server/test").Start(r.Context(), "users.lookup")
		defer span.End()
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/users/u-1", nil)
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const parentID = "00f067aa0ba902b7"
	req.Header.Set("traceparent", "00-"+traceID+"-"+parentID+"-01")
	response := httptest.NewRecorder()
	WrapHandler("fallback-service", mux).ServeHTTP(response, req)
	if response.Code != http.StatusOK || response.Header().Get("X-Trace-Id") != traceID {
		t.Fatalf("response does not retain incoming trace: %d %v", response.Code, response.Header())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var export *collectortrace.ExportTraceServiceRequest
	select {
	case export = <-received:
	case <-ctx.Done():
		t.Fatal("no OTLP export received on shutdown")
	}
	var server, child *tracepb.Span
	for _, resourceSpans := range export.ResourceSpans {
		attrs := make(map[string]string)
		for _, attr := range resourceSpans.Resource.Attributes {
			attrs[attr.Key] = attr.Value.GetStringValue()
		}
		if attrs["service.name"] != "configured-service" || attrs["deployment.environment.name"] != "lab" {
			t.Fatalf("incorrect resource attributes: %v", attrs)
		}
		for _, scope := range resourceSpans.ScopeSpans {
			for _, span := range scope.Spans {
				if hex.EncodeToString(span.TraceId) != traceID {
					t.Fatalf("exported span belongs to a different trace: %v", span)
				}
				if span.Kind == tracepb.Span_SPAN_KIND_SERVER {
					server = span
				}
				if span.Name == "users.lookup" {
					child = span
				}
			}
		}
	}
	if server == nil || child == nil {
		t.Fatalf("expected HTTP and child spans: %v", export)
	}
	if hex.EncodeToString(server.ParentSpanId) != parentID || hex.EncodeToString(child.ParentSpanId) != hex.EncodeToString(server.SpanId) {
		t.Fatalf("broken span hierarchy: server=%v child=%v", server, child)
	}
}

func TestTelemetryConsoleExporter(t *testing.T) {
	// The exporter captures stdout when its package is initialized. A fresh
	// process tests real stdout export without replacing a package-level writer.
	if os.Getenv("GO_SERVER_CONSOLE_TEST_CHILD") == "1" {
		shutdown, err := InitTelemetry("console-test")
		if err != nil {
			t.Fatal(err)
		}
		defer shutdownTelemetry(shutdown)
		_, span := otel.Tracer("go-server/test").Start(t.Context(), "console.operation")
		span.End()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTelemetryConsoleExporter$")
	cmd.Env = append(os.Environ(), "GO_SERVER_CONSOLE_TEST_CHILD=1", "OTEL_TRACES_EXPORTER=console",
		"OTEL_TRACES_SAMPLER=always_on", "OTEL_SERVICE_NAME=console-test", "OTEL_RESOURCE_ATTRIBUTES=")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("console export subprocess: %v", err)
	}
	var exported struct{ Name string }
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	if exported.Name != "console.operation" {
		t.Fatalf("unexpected console span: %+v", exported)
	}
}

func TestTelemetryRejectsUnsupportedExporter(t *testing.T) {
	preserveTelemetryGlobals(t)
	t.Setenv("OTEL_TRACES_EXPORTER", "unsupported")
	if _, err := InitTelemetry("invalid-test"); err == nil {
		t.Fatal("expected an error for an unsupported exporter")
	}
}
