package xops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/storage"
	cryptoSSH "golang.org/x/crypto/ssh"
)

type PrivilegeTestResult struct {
	Escalated bool   `json:"escalated"`
	SudoMode  string `json:"sudoMode"`
	User      string `json:"user"`
	Detail    string `json:"detail,omitempty"`
}

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

func (m *Materials) TestPrivilege(ctx context.Context, permit ports.Permit, nodeID string) (_ *PrivilegeTestResult, retErr error) {
	work, cancel, err := ports.WorkContext(ctx, permit, ports.Inspect)
	if err != nil {
		return nil, err
	}
	defer cancel()
	_, target, err := permit.Snapshot().Resolve(nodeID)
	if err != nil {
		return nil, err
	}
	return m.ProbePrivilege(work, target.Plan, target.Execution, nil)
}

func (m *Materials) ProbePrivilege(ctx context.Context, plan ssh.ConnectionPlan, execution *ssh.ExecutionConfig, sources []storage.Source) (_ *PrivilegeTestResult, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var connector *ssh.Connector
	if len(sources) > 0 {
		draft := &Materials{Store: probeSources{Repository: m.Store, sources: sources}, Vault: m.Vault, DomainID: m.DomainID}
		connector = ssh.NewConnector(nil, ssh.WithSecretResolver(draft), ssh.WithKeySource(draft), ssh.WithHostKeyVerifier(draft), ssh.WithHandshakeTimeout(5*time.Second))
	} else {
		connector = ssh.NewConnector(nil, ssh.WithSecretResolver(m), ssh.WithKeySource(m), ssh.WithHostKeyVerifier(m), ssh.WithHandshakeTimeout(5*time.Second))
	}
	defer func() { retErr = errors.Join(retErr, connector.CloseAll()) }()
	connection, err := connector.ConnectPlan(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, connection.Close()) }()

	if len(plan.Hops) == 0 {
		return nil, errors.New("empty connection hops")
	}
	targetHop := plan.Hops[len(plan.Hops)-1]
	mode := targetHop.SudoMode
	// Resolve an unspecified environment through the shared core before any
	// identity command runs; keep explicit settings and the stored node intact.
	execution, err = connection.Client.ProbeControlledExecution(ctx, execution)
	if err != nil {
		return nil, fmt.Errorf("探测提权执行环境失败: %w", err)
	}

	if mode == "" || mode == ssh.SudoModeNone {
		run := func(command string) (string, error) {
			opts := execution.CommandOptions()
			opts.Timeout, opts.OutputLimit = 15*time.Second, 4096
			commandPlan, err := ssh.PlanCommand(command, opts)
			if err != nil {
				return "", fmt.Errorf("plan privilege diagnostic: %w", err)
			}
			result := connection.Client.ExecuteCommand(ctx, commandPlan)
			return result.Output, result.Err()
		}
		out, err := run("id -u && id -un")
		if err != nil {
			return nil, fmt.Errorf("执行登录身份检查失败: %w", err)
		}
		if user, err := privilegeRootUser(out); err == nil {
			return &PrivilegeTestResult{
				Escalated: true,
				SudoMode:  "root",
				User:      user,
				Detail:    "当前登录身份即为 root 用户",
			}, nil
		}

		out, err = run("sudo -n id -u && sudo -n id -un")
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, errors.New("节点未配置提权方式（当前为 none）且无法验证免密 root 提权权限")
		}
		user, err := privilegeRootUser(out)
		if err != nil {
			return nil, err
		}
		return &PrivilegeTestResult{
			Escalated: true,
			SudoMode:  "sudoer",
			User:      user,
			Detail:    "远端具备免密 sudo (sudoer) 权限，建议在配置中开启",
		}, nil
	}

	out, err := connection.Client.RunWithSudoExecution(ctx, "id -u && id -un", execution)
	if err != nil {
		return nil, fmt.Errorf("提权执行失败: %w", err)
	}
	user, err := privilegeRootUser(out)
	if err != nil {
		return nil, err
	}
	return &PrivilegeTestResult{
		Escalated: true,
		SudoMode:  string(mode),
		User:      user,
	}, nil
}

// Login banners may precede the two fixed diagnostic lines. Never infer root
// from a successful exit alone or substitute a default user for missing output.
func privilegeRootUser(output string) (string, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		return "", errors.New("无法验证提权身份：远端未返回完整的 UID 和用户名")
	}
	if strings.TrimSpace(lines[len(lines)-2]) != "0" {
		return "", errors.New("提权身份检查失败：实际 UID 不是 0")
	}
	return strings.TrimSpace(lines[len(lines)-1]), nil
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
