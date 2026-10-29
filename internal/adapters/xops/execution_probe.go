package xops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/storage"
)

// probeSources keeps draft endpoint bindings in memory. Credential versions are
// still read from the deployment store; probing never persists draft sources.
type probeSources struct {
	storage.Repository
	sources []storage.Source
}

func (s probeSources) Source(ctx context.Context, q storage.Source) (storage.Source, error) {
	if err := ctx.Err(); err != nil {
		return storage.Source{}, err
	}
	for _, source := range s.sources {
		if source.Token == q.Token && source.NodeID == q.NodeID && source.Host == q.Host && source.Port == q.Port && source.User == q.User && source.Purpose == q.Purpose {
			return source, nil
		}
	}
	return storage.Source{}, ssh.ErrSnapshotMismatch
}

// ExecutionProbeResult wraps ProbeResult with captured protocol diagnostics.
type ExecutionProbeResult struct {
	*ssh.ProbeResult
	Diagnostics []DiagnosticEvent `json:"diagnostics,omitempty"`
}

// ProbeExecution inspects an administrator-authorized immutable draft plan using
// the deployment's pinned host keys and versioned credentials. Only the fixed
// core probes run; the caller owns revision checks and audit admission.
func (m *Materials) ProbeExecution(ctx context.Context, plan ssh.ConnectionPlan, sources []storage.Source) (_ *ExecutionProbeResult, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	collector := NewDiagnosticCollector()
	draft := &Materials{Store: probeSources{Repository: m.Store, sources: sources}, Vault: m.Vault, DomainID: m.DomainID}
	connector := ssh.NewConnector(nil,
		ssh.WithSecretResolver(draft),
		ssh.WithKeySource(draft),
		ssh.WithHostKeyVerifier(draft),
		ssh.WithHandshakeTimeout(10*time.Second),
		ssh.WithDiagnostics(collector),
	)
	defer func() { retErr = errors.Join(retErr, connector.CloseAll()) }()
	connection, err := connector.ConnectPlan(ctx, plan)
	if err != nil {
		return &ExecutionProbeResult{Diagnostics: collector.Events()}, fmt.Errorf("connect execution probe: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, connection.Close()) }()
	result, err := connection.Client.ProbeExecutionConfig(ctx)
	if err != nil {
		return &ExecutionProbeResult{Diagnostics: collector.Events()}, fmt.Errorf("probe execution environment: %w", err)
	}
	return &ExecutionProbeResult{
		ProbeResult: result,
		Diagnostics: collector.Events(),
	}, nil
}
