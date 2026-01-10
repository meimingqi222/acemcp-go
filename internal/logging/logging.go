package logging

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger wraps zap.Logger with minimal helpers used across the project.
type Logger struct {
	*zap.Logger
}

// NewLogger creates a zap logger with console encoding and configurable level.
func NewLogger(level string) (*Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.Encoding = "console"
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder

	lvl := zapcore.InfoLevel
	if err := lvl.Set(level); err == nil {
		cfg.Level = zap.NewAtomicLevelAt(lvl)
	}

	z, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return &Logger{z}, nil
}

// Helper field constructors to avoid direct zap import at call sites.
func String(key, val string) zap.Field  { return zap.String(key, val) }
func Error(err error) zap.Field         { return zap.Error(err) }
func Int(key string, val int) zap.Field { return zap.Int(key, val) }
func Any(key string, val any) zap.Field { return zap.Any(key, val) }

// Convenience wrappers for common levels.
func (l *Logger) Debug(msg string, fields ...zap.Field) { l.Logger.Debug(msg, fields...) }
func (l *Logger) Info(msg string, fields ...zap.Field)  { l.Logger.Info(msg, fields...) }
func (l *Logger) Warn(msg string, fields ...zap.Field)  { l.Logger.Warn(msg, fields...) }
func (l *Logger) Error(msg string, fields ...zap.Field) { l.Logger.Error(msg, fields...) }
