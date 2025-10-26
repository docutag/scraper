package logging

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/zombar/purpletab/pkg/tracing"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	status      int
	bytesWritten int64
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// HTTPLoggingMiddleware logs HTTP requests in structured JSON format
func HTTPLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status
			wrapped := &responseWriter{
				ResponseWriter: w,
				status:        http.StatusOK,
				bytesWritten:  0,
			}

			// Get trace context if available
			traceID := tracing.TraceIDFromContext(r.Context())
			spanID := tracing.SpanIDFromContext(r.Context())

			// Call next handler
			next.ServeHTTP(wrapped, r)

			// Calculate request duration
			duration := time.Since(start)

			// Log structured request
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http_request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", wrapped.status),
				slog.Int64("bytes", wrapped.bytesWritten),
				slog.Float64("duration_ms", float64(duration.Milliseconds())),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
				slog.String("referer", r.Referer()),
				slog.String("trace_id", traceID),
				slog.String("span_id", spanID),
				slog.String("protocol", r.Proto),
				slog.String("host", r.Host),
			)
		})
	}
}

// HTTPErrorLogger logs HTTP errors in structured format
func HTTPErrorLogger(logger *slog.Logger, statusCode int, err error, r *http.Request) {
	traceID := tracing.TraceIDFromContext(r.Context())
	spanID := tracing.SpanIDFromContext(r.Context())

	logger.LogAttrs(r.Context(), slog.LevelError, "http_error",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", statusCode),
		slog.String("error", err.Error()),
		slog.String("trace_id", traceID),
		slog.String("span_id", spanID),
		slog.String("remote_addr", r.RemoteAddr),
	)
}

// LogRequest logs a simple request event
func LogRequest(logger *slog.Logger, r *http.Request, msg string, attrs ...slog.Attr) {
	traceID := tracing.TraceIDFromContext(r.Context())
	spanID := tracing.SpanIDFromContext(r.Context())

	baseAttrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("trace_id", traceID),
		slog.String("span_id", spanID),
	}

	allAttrs := append(baseAttrs, attrs...)
	logger.LogAttrs(r.Context(), slog.LevelInfo, msg, allAttrs...)
}
