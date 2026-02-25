package logging

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger wraps zap.Logger with minimal helpers used across the project.
type Logger struct {
	*zap.Logger
}

// NewLogger creates a zap logger with console encoding and configurable level.
func NewLogger(level string) (*Logger, error) {
	return NewLoggerWithOutput(level, os.Stderr)
}

// NewLoggerWithOutput creates a logger that writes to the specified output.
// For MCP servers, use os.Stderr to avoid polluting stdout (used for JSON-RPC).
func NewLoggerWithOutput(level string, output *os.File) (*Logger, error) {
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder

	lvl := zapcore.InfoLevel
	_ = lvl.Set(level) // keep default info when level is invalid

	sink := zapcore.AddSync(output)
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encCfg),
		sink,
		lvl,
	)
	z := zap.New(core, zap.ErrorOutput(sink))
	return &Logger{z}, nil
}

// Helper field constructors to avoid direct zap import at call sites.
func String(key, val string) zap.Field      { return zap.String(key, val) }
func Error(err error) zap.Field             { return zap.Error(err) }
func Int(key string, val int) zap.Field     { return zap.Int(key, val) }
func Int64(key string, val int64) zap.Field { return zap.Int64(key, val) }
func Any(key string, val any) zap.Field     { return zap.Any(key, val) }

// Convenience wrappers for common levels.
func (l *Logger) Debug(msg string, fields ...zap.Field) { l.Logger.Debug(msg, fields...) }
func (l *Logger) Info(msg string, fields ...zap.Field)  { l.Logger.Info(msg, fields...) }
func (l *Logger) Warn(msg string, fields ...zap.Field)  { l.Logger.Warn(msg, fields...) }
func (l *Logger) Error(msg string, fields ...zap.Field) { l.Logger.Error(msg, fields...) }
