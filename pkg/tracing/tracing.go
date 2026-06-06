// Package tracing initializes OpenTelemetry tracing.
package tracing

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

// Init configures the global tracer provider. When no OTLP endpoint is set,
// tracing remains no-op so local tests and demo runs do not need a collector.
func Init(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	if serviceName == "" {
		serviceName = "shortvideo"
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, err
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.NeverSample()),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		return tp.Shutdown, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
