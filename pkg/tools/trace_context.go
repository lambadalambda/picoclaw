package tools

import "context"

type traceContextKey struct{}

// WithTraceID attaches a correlation trace ID to context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if traceID == "" {
		return ctx
	}
	return context.WithValue(ctx, traceContextKey{}, traceID)
}

// TraceIDFromContext extracts a correlation trace ID from context.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(traceContextKey{}).(string)
	return v
}
