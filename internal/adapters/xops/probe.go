package xops

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/storage"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func (m *Materials) TestConnection(ctx context.Context, permit ports.Permit, nodeID string) (retErr error) {
	work, cancel, err := ports.WorkContext(ctx, permit, ports.Inspect)
	if err != nil {
		return err
	}
	defer cancel()
	_, target, err := permit.Snapshot().Resolve(nodeID)
	if err != nil {
		return err
	}
	connector := ssh.NewConnector(nil, ssh.WithSecretResolver(m), ssh.WithKeySource(m), ssh.WithHostKeyVerifier(m), ssh.WithHandshakeTimeout(5*time.Second))
	defer func() { retErr = errors.Join(retErr, connector.CloseAll()) }()
	connection, err := connector.ConnectPlan(work, target.Plan)
	if err != nil {
		return fmt.Errorf("SSH connection test failed: %w", err)
	}
	return connection.Close()
}

var errObserved = errors.New("host key observed; authentication intentionally not attempted")

type keyObservation struct {
	mu         sync.Mutex
	key        cryptoSSH.PublicKey
	algorithms []string
}

func (o *keyObservation) Verify(ctx context.Context, _ ssh.HostKeyRequest, key cryptoSSH.PublicKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	o.key = key
	o.mu.Unlock()
	return errObserved
}
func (o *keyObservation) HostKeyAlgorithms(ctx context.Context, _ ssh.HostKeyRequest) ([]string, error) {
	return o.algorithms, ctx.Err()
}

type probeSecret struct{}

func (probeSecret) ResolveSecret(ctx context.Context, _ ssh.SecretRequest) ([]byte, error) {
	return []byte("unused-host-key-probe"), ctx.Err()
}

// ProbeHostKey terminates at host-key verification, before SSH authentication.
// The observed public key is untrusted until a separate administrator action.
func ProbeHostKey(ctx context.Context, host storage.Host, algorithm string) (_ cryptoSSH.PublicKey, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	algorithms := cryptoSSH.SupportedAlgorithms().HostKeys
	if algorithm != "" {
		algorithms = []string{algorithm}
	}
	observation := &keyObservation{algorithms: algorithms}
	connector := ssh.NewConnector(nil, ssh.WithSecretResolver(probeSecret{}), ssh.WithHostKeyVerifier(observation), ssh.WithHandshakeTimeout(5*time.Second))
	defer func() { retErr = errors.Join(retErr, connector.CloseAll()) }()
	connection, err := connector.ConnectPlan(ctx, ssh.ConnectionPlan{Scope: "admin-host-probe", Hops: []ssh.ConnectionConfig{{NodeID: host.ID, Address: host.Address, Port: host.Port, User: "host-key-probe", AuthType: "password", AuthUpdateToken: "probe", TrustVersion: "probe"}}})
	if connection != nil {
		if closeErr := connection.Close(); closeErr != nil {
			return nil, closeErr
		}
	}
	if !errors.Is(err, errObserved) {
		return nil, fmt.Errorf("host key probe failed: %w", err)
	}
	observation.mu.Lock()
	defer observation.mu.Unlock()
	if observation.key == nil {
		return nil, errors.New("host key probe returned no key")
	}
	return observation.key, nil
}
