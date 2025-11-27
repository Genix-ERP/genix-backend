package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"time"
)

// Logger is the application logger interface
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	With(args ...any) Logger
	WithContext(ctx context.Context) Logger
}

// logger implements the Logger interface using slog
type logger struct {
	*slog.Logger
}

// NewLogger creates a new logger instance
func NewLogger(level, format string) Logger {
	var handler slog.Handler
	var output io.Writer = os.Stdout

	opts := &slog.HandlerOptions{
		Level:     parseLevel(level),
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize time format
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format(time.RFC3339))
				}
			}
			// Shorten source path
			if a.Key == slog.SourceKey {
				if source, ok := a.Value.Any().(*slog.Source); ok {
					a.Value = slog.StringValue(shortSource(source))
				}
			}
			return a
		},
	}

	switch format {
	case "json":
		handler = slog.NewJSONHandler(output, opts)
	default:
		handler = slog.NewTextHandler(output, opts)
	}

	return &logger{slog.New(handler)}
}

// parseLevel converts a string level to slog.Level
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// shortSource returns a shortened source location
func shortSource(source *slog.Source) string {
	if source == nil {
		return ""
	}
	// Get just the file name without the full path
	_, file := splitPath(source.File)
	return file + ":" + itoa(source.Line)
}

func splitPath(path string) (dir, file string) {
	i := len(path) - 1
	for i >= 0 && path[i] != '/' && path[i] != '\\' {
		i--
	}
	return path[:i+1], path[i+1:]
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// Debug logs a debug message
func (l *logger) Debug(msg string, args ...any) {
	l.Logger.Debug(msg, args...)
}

// Info logs an info message
func (l *logger) Info(msg string, args ...any) {
	l.Logger.Info(msg, args...)
}

// Warn logs a warning message
func (l *logger) Warn(msg string, args ...any) {
	l.Logger.Warn(msg, args...)
}

// Error logs an error message
func (l *logger) Error(msg string, args ...any) {
	l.Logger.Error(msg, args...)
}

// With creates a child logger with the given attributes
func (l *logger) With(args ...any) Logger {
	return &logger{l.Logger.With(args...)}
}

// WithContext creates a child logger with context values
func (l *logger) WithContext(ctx context.Context) Logger {
	// Extract common context values
	attrs := []any{}

	if requestID := ctx.Value(ContextKeyRequestID); requestID != nil {
		attrs = append(attrs, "request_id", requestID)
	}
	if tenantID := ctx.Value(ContextKeyTenantID); tenantID != nil {
		attrs = append(attrs, "tenant_id", tenantID)
	}
	if userID := ctx.Value(ContextKeyUserID); userID != nil {
		attrs = append(attrs, "user_id", userID)
	}

	if len(attrs) > 0 {
		return l.With(attrs...)
	}
	return l
}

// Context keys for logging
type contextKey string

const (
	ContextKeyRequestID contextKey = "request_id"
	ContextKeyTenantID  contextKey = "tenant_id"
	ContextKeyUserID    contextKey = "user_id"
	ContextKeyLogger    contextKey = "logger"
)

// FromContext retrieves the logger from context
func FromContext(ctx context.Context) Logger {
	if l, ok := ctx.Value(ContextKeyLogger).(Logger); ok {
		return l
	}
	return NewLogger("info", "json")
}

// ToContext adds the logger to context
func ToContext(ctx context.Context, l Logger) context.Context {
	return context.WithValue(ctx, ContextKeyLogger, l)
}

// LogPanic logs panic information and stack trace
func LogPanic(l Logger, recovered interface{}) {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	l.Error("panic recovered",
		"error", recovered,
		"stack", string(buf[:n]),
	)
}

// LogRequest logs HTTP request information
func LogRequest(l Logger, method, path, clientIP string, statusCode int, latency time.Duration, bodySize int) {
	l.Info("http_request",
		"method", method,
		"path", path,
		"client_ip", clientIP,
		"status", statusCode,
		"latency_ms", latency.Milliseconds(),
		"body_size", bodySize,
	)
}

// LogDatabaseQuery logs database query information
func LogDatabaseQuery(l Logger, query string, args []interface{}, duration time.Duration, err error) {
	attrs := []any{
		"query", truncateString(query, 500),
		"duration_ms", duration.Milliseconds(),
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
		l.Error("database_query", attrs...)
	} else {
		l.Debug("database_query", attrs...)
	}
}

// LogExternalAPI logs external API call information
func LogExternalAPI(l Logger, service, method, url string, statusCode int, duration time.Duration, err error) {
	attrs := []any{
		"service", service,
		"method", method,
		"url", url,
		"duration_ms", duration.Milliseconds(),
	}
	if statusCode > 0 {
		attrs = append(attrs, "status_code", statusCode)
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
		l.Error("external_api_call", attrs...)
	} else {
		l.Info("external_api_call", attrs...)
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
