package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/meimingqi222/acemcp-go/internal/config"
	"github.com/meimingqi222/acemcp-go/internal/indexer"
	"github.com/meimingqi222/acemcp-go/internal/logging"
	"github.com/meimingqi222/acemcp-go/internal/state"
	"github.com/meimingqi222/acemcp-go/internal/version"
)

//go:embed templates/index.html
var indexHTML []byte

// HTTPServer provides health/management endpoints.
type HTTPServer struct {
	addr   string
	logger *logging.Logger
	srv    *http.Server
}

func withOptionalAuth(token string, h http.HandlerFunc) http.HandlerFunc {
	if token == "" {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Acemcp-Token") != token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized"))
			return
		}
		h(w, r)
	}
}

func NewHTTPServer(cfg *config.Config, st *state.State, idx *indexer.Service, logger *logging.Logger) *HTTPServer {
	mux := http.NewServeMux()

	// Web UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"status": string(st.Status()),
			"data": map[string]any{
				"http":   cfg.HTTPAddr,
				"listen": cfg.Listen,
				"data":   cfg.DataDir,
				"base":   cfg.BaseURL,
				"batch":  cfg.BatchSize,
				"maxln":  cfg.MaxLinesPerBlob,
				"ver":    version.Version,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/projects", withOptionalAuth(cfg.HTTPToken, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		projects, err := idx.ListProjects()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(projects)
	}))

	mux.HandleFunc("/failed", withOptionalAuth(cfg.HTTPToken, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		failed, err := idx.ListFailed()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(failed)
	}))

	mux.HandleFunc("/metrics", withOptionalAuth(cfg.HTTPToken, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(idx.Metrics())
	}))

	mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Support ?after=ID for incremental fetching
		afterStr := r.URL.Query().Get("after")
		if afterStr != "" {
			var afterID int64
			fmt.Sscanf(afterStr, "%d", &afterID)
			_ = json.NewEncoder(w).Encode(idx.LogsSince(afterID))
			return
		}
		// Default: return recent 50 logs
		_ = json.NewEncoder(w).Encode(idx.RecentLogs(50))
	})

	mux.HandleFunc("/watchers", withOptionalAuth(cfg.HTTPToken, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		diags, err := idx.GetWatcherDiagnostics()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(diags)
	}))

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	return &HTTPServer{addr: cfg.HTTPAddr, logger: logger, srv: srv}
}

func (s *HTTPServer) Start() error {
	s.logger.Info("http server starting", logging.String("addr", s.addr))
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}

func (s *HTTPServer) Shutdown(ctx context.Context) error {
	s.logger.Info("http server shutting down")
	return s.srv.Shutdown(ctx)
}
