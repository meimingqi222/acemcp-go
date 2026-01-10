package rpc

import (
	"encoding/json"

	"github.com/meimingqi222/acemcp-go/internal/config"
	"github.com/meimingqi222/acemcp-go/internal/indexer"
	"github.com/meimingqi222/acemcp-go/internal/state"
	"github.com/meimingqi222/acemcp-go/internal/version"
)

// RegisterCore registers core handlers.
// Currently calls indexer/search stubs; replace with real implementations later.
func (s *Server) RegisterCore(cfg *config.Config, st *state.State, idx *indexer.Service) {
	type indexParams struct {
		ProjectRootPath string `json:"project_root_path"`
	}
	type searchParams struct {
		ProjectRootPath string `json:"project_root_path"`
		Query           string `json:"query"`
	}

	s.Register("IndexProject", func(params json.RawMessage) (any, *Error) {
		var p indexParams
		if err := json.Unmarshal(params, &p); err != nil || p.ProjectRootPath == "" {
			return nil, &Error{Code: -32602, Message: "invalid params: project_root_path required"}
		}
		res, err := idx.IndexProject(p.ProjectRootPath)
		if err != nil {
			return nil, &Error{Code: -32001, Message: err.Error()}
		}
		return res, nil
	})

	s.Register("SearchContext", func(params json.RawMessage) (any, *Error) {
		var p searchParams
		if err := json.Unmarshal(params, &p); err != nil || p.ProjectRootPath == "" || p.Query == "" {
			return nil, &Error{Code: -32602, Message: "invalid params: project_root_path and query required"}
		}
		res, err := idx.SearchContext(p.ProjectRootPath, p.Query)
		if err != nil {
			return nil, &Error{Code: -32002, Message: err.Error()}
		}
		return res, nil
	})

	s.Register("GetStatus", func(params json.RawMessage) (any, *Error) {
		return map[string]any{
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
		}, nil
	})

	s.Register("ReloadConfig", func(params json.RawMessage) (any, *Error) {
		if err := cfg.Reload(); err != nil {
			return nil, &Error{Code: -32000, Message: "reload failed: " + err.Error()}
		}
		idx.RefreshAllowedExts()
		return map[string]any{
			"status":  "ok",
			"message": "config reloaded",
			"base":    cfg.BaseURL,
			"batch":   cfg.BatchSize,
			"maxln":   cfg.MaxLinesPerBlob,
		}, nil
	})
}
