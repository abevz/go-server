package platform

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// InitTelemetry configures W3C propagation and optional span export.
// OTEL_TRACES_EXPORTER selects none (default), console, or otlp (HTTP/Protobuf).
// The OTLP exporter and SDK read their standard endpoint, resource, sampling,
// and batch-processor environment variables.
func InitTelemetry(serviceName string) (func(context.Context) error, error) {
	res, err := resource.New(context.Background(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attribute.String("service.name", serviceName)),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("configure telemetry resource: %w", err)
	}
	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	var exporter sdktrace.SpanExporter
	switch name := os.Getenv("OTEL_TRACES_EXPORTER"); name {
	case "", "none":
	case "console":
		exporter, err = stdouttrace.New()
	case "otlp":
		exporter, err = otlptracehttp.New(context.Background())
	default:
		return nil, fmt.Errorf("unsupported OTEL_TRACES_EXPORTER %q: use none, console, or otlp", name)
	}
	if err != nil {
		return nil, fmt.Errorf("create span exporter: %w", err)
	}
	if exporter != nil {
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}
	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

func shutdownTelemetry(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		slog.Warn("flush telemetry on shutdown", "err", err)
	}
}
