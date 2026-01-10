package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourorg/acemcp-go/internal/config"
	"github.com/yourorg/acemcp-go/internal/indexer"
	"github.com/yourorg/acemcp-go/internal/logging"
	"github.com/yourorg/acemcp-go/internal/rpc"
	"github.com/yourorg/acemcp-go/internal/server"
	"github.com/yourorg/acemcp-go/internal/state"
)

func main() {
	// CLI flags (override config file)
	listen := flag.String("listen", "127.0.0.1:7033", "JSON-RPC listen address (placeholder)")
	httpAddr := flag.String("http", "127.0.0.1:7034", "HTTP management/health address")
	dataDir := flag.String("data", "", "Data directory (defaults to ~/.acemcp/data)")
	logLevel := flag.String("log-level", "info", "Log level: debug|info|warn|error")
	flag.Parse()

	cfg, err := config.Load(*dataDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// CLI overrides
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *httpAddr != "" {
		cfg.HTTPAddr = *httpAddr
	}
	if *logLevel != "" {
		cfg.LogLevel = *logLevel
	}

	logger, err := logging.NewLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("acemcp-go daemon starting",
		logging.String("listen", cfg.Listen),
		logging.String("http", cfg.HTTPAddr),
		logging.String("data", cfg.DataDir),
		logging.String("settings", cfg.SettingsPath),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st := state.New()
	idx := indexer.New(cfg, logger)
	httpSrv := server.NewHTTPServer(cfg, st, idx, logger)
	rpcSrv := rpc.New(cfg.Listen, logger)
	rpcSrv.RegisterCore(cfg, st, idx)

	errCh := make(chan error, 2)
	go func() {
		if err := httpSrv.Start(); err != nil {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	go func() {
		if err := rpcSrv.Start(); err != nil {
			errCh <- fmt.Errorf("rpc server: %w", err)
		}
	}()

	// Mark as ready after servers start
	st.SetReady()
	logger.Info("acemcp-go daemon ready")

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		st.SetStopping()
	case err := <-errCh:
		logger.Error("server error", logging.Error(err))
		st.SetStopping()
		// Shutdown both servers on error to avoid resource leak
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		_ = rpcSrv.Shutdown(shutdownCtx)
		idx.StopAll()
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", logging.Error(err))
	}
	if err := rpcSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("rpc shutdown error", logging.Error(err))
	}
	idx.StopAll()

	logger.Info("acemcp-go daemon stopped")
}
