package server_test

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/testutil"
	cryptoSSH "golang.org/x/crypto/ssh"
)

// The peer emulates probe frames and exit status; it never executes local commands.
func TestExecutionProbeAndStructuredResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var probes, userCommands atomic.Int32
	peer, err := sshfixture.NewWithOptions(ctx, sshfixture.Options{ExecHandler: func(command string, channel cryptoSSH.Channel) {
		output := "command-output"
		status := uint32(0)
		switch {
		case strings.Contains(command, "__XOPS_DIALECT_POSIX__"):
			probes.Add(1)
			output = "__XOPS_DIALECT_POSIX__:Linux:/bin/sh\n"
		case strings.Contains(command, "__XOPS_INTERP_END__"):
			probes.Add(1)
			output = "__XOPS_INTERP__:sh\n__XOPS_INTERP_END__\n"
		default:
			userCommands.Add(1)
			if strings.Contains(command, "failure") {
				status = 7
			}
		}
		if _, err := io.WriteString(channel, output); err != nil {
			t.Error(err)
			return
		}
		if _, err := channel.SendRequest("exit-status", false, cryptoSSH.Marshal(struct{ Status uint32 }{status})); err != nil {
			t.Error(err)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	doc := fixtureDocument(t, peer)
	node := doc.Nodes["peer"]
	node.Execution = nil
	doc.Nodes["peer"] = node
	r := start(t, ctx, testutil.Config(t), &doc)
	for i, command := range []string{"success", "failure"} {
		var output mcpruntime.SshRunOutput
		call(t, ctx, r, "xops_ssh_run", map[string]any{"nodeID": "peer", "command": command}, &output)
		want := uint32(0)
		if i == 1 {
			want = 7
		}
		if output.Outcome != "completed" || output.ExitCode == nil || *output.ExitCode != want || output.PlanDigest == "" || output.Output != "command-output" {
			t.Fatalf("unexpected execution result: %+v", output)
		}
		if (output.Status == "success") != (want == 0) {
			t.Fatalf("exit status not reflected: %+v", output)
		}
	}
	if probes.Load() != 2 || userCommands.Load() != 2 {
		t.Fatalf("probe cache or dispatch mismatch: probes=%d commands=%d", probes.Load(), userCommands.Load())
	}
	v, err := r.host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range v.Nodes {
		if node.Execution != nil {
			t.Fatal("transient probe persisted execution settings")
		}
	}
	result, err := r.session.CallTool(ctx, &mcp.CallToolParams{Name: "xops_ssh_run", Arguments: map[string]any{"nodeID": "peer", "command": "invalid", "execution": map[string]any{"interpreter": "cmd", "launchDialect": "posix"}}})
	if err == nil && !result.IsError {
		t.Fatal("invalid execution dispatched")
	}
	if userCommands.Load() != 2 {
		t.Fatal("validation failure reached SSH")
	}
	var explicit mcpruntime.SshRunOutput
	call(t, ctx, r, "xops_ssh_run", mcpruntime.SshRunInput{NodeID: "peer", Command: "passthrough", Execution: &ssh.ExecutionConfig{Interpreter: ssh.InterpreterServer}}, &explicit)
	if probes.Load() != 2 {
		t.Fatal("explicit server mode probed")
	}
}
