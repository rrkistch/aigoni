package server

import (
	"aigoni/internal/agent"
	"context"
	"time"
)

// Called with wikiAgentMu held; old runs keep their captured runner.
func (s *Server) configuredAIRunner(ctx context.Context, p aiProvider, version string, readOnly bool) (agent.Runner, error) {
	if readOnly && s.aiReadRunner != nil && s.aiReadVersion == version {
		return s.aiReadRunner, nil
	}
	if !readOnly && s.aiWriteRunner != nil && s.aiWriteVersion == version {
		return s.aiWriteRunner, nil
	}
	cfg := agent.Config{Root: s.root, APIKey: p.Key, BaseURL: p.BaseURL, Model: p.Model, ReadOnly: readOnly, HTTPClient: aiHTTPClient(p, 10*time.Minute)}
	if !readOnly {
		cfg.WriteLock = &s.wikiWriteMu
	}
	runner, err := agent.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if readOnly {
		s.aiReadRunner = runner
		s.aiReadVersion = version
	} else {
		s.aiWriteRunner = runner
		s.aiWriteVersion = version
	}
	return runner, nil
}
