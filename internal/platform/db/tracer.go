package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// queryTracer implements pgx.QueryTracer. It always attaches to the pool
// (see NewPostgresPool) and reads from whatever TracerProvider otel.Setup
// registered globally — a no-op provider when telemetry is disabled, same
// pattern platform/cache/redis.go uses for redisotel. data.SQL is the
// parameterized query text (placeholders, not bound values), so no
// argument values ever reach a span attribute.
type queryTracer struct {
	tracer trace.Tracer
}

func newQueryTracer() *queryTracer {
	return &queryTracer{tracer: otel.Tracer("stratum/postgres")}
}

type spanCtxKey struct{}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, span := t.tracer.Start(ctx, "postgres.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.statement", data.SQL),
		),
	)
	return context.WithValue(ctx, spanCtxKey{}, span)
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(spanCtxKey{}).(trace.Span)
	if !ok {
		return
	}
	defer span.End()

	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
	}
}
