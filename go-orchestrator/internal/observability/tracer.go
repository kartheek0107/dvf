// Package observability provides distributed tracing for the DVF orchestrator.
//
// Traces are exported via OTLP gRPC to Jaeger/Tempo when
// cfg.TraceEndpoint is non-empty. If the endpoint is empty (dev mode),
// a stdout exporter is used so traces are visible in logs without any
// infrastructure.
//
// Usage:
//
//	tp, shutdown, err := observability.InitTracer(cfg.Telemetry)
//	defer shutdown(context.Background())
//	tracer := observability.Tracer("dvf.orchestrator")
//	ctx, span := tracer.Start(ctx, "operation")
//	defer span.End()
package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartheekbudime/driver-validation-suite/go-orchestrator/internal/config"
)

const serviceName = "dvf-orchestrator"

// InitTracer configures and returns an OpenTelemetry TracerProvider.
//
// If cfg.TraceEndpoint is non-empty, spans are exported via OTLP gRPC.
// If it is empty, spans are written to stdout (dev/no-infra mode).
//
// The returned shutdown func must be called on program exit to flush
// any buffered spans.
func InitTracer(cfg config.TelemetryConfig) (trace.TracerProvider, func(context.Context), error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"",
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "production"),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTel resource: %w", err)
	}

	var exporter sdktrace.SpanExporter

	if cfg.TraceEndpoint != "" {
		// OTLP gRPC exporter → Jaeger/Tempo
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		exp, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.TraceEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("creating OTLP exporter: %w", err)
		}
		exporter = exp
	} else {
		// Stdout exporter — no infrastructure required
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, nil, fmt.Errorf("creating stdout exporter: %w", err)
		}
		exporter = exp
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set the global TracerProvider and propagator
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdown := func(ctx context.Context) {
		_ = tp.Shutdown(ctx)
	}

	return tp, shutdown, nil
}

// Tracer returns a named tracer from the global TracerProvider.
// Call InitTracer first to configure the provider.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
