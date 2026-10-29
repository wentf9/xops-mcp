package contract_test

import (
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
	"github.com/wentf9/xops-mcp/internal/testutil"
	"testing"
)

func TestExecutionConfigurationPersistenceAndArchive(t *testing.T) {
	for _, source := range []string{"sqlite", "postgres"} {
		for _, target := range []string{"sqlite", "postgres"} {
			t.Run(source+"-"+target, func(t *testing.T) {
				s, vault := open(t, source)
				doc := testutil.Document(t)
				node := doc.Nodes["peer"]
				login := false
				node.Execution = &ssh.ExecutionConfig{Interpreter: ssh.InterpreterZsh, LaunchDialect: ssh.LaunchPOSIX, Login: &login}
				node.PasswordPromptPattern = `(?i)password for .*:`
				doc.Nodes["peer"] = node
				v, report := testutil.Plan(t, s, vault, doc)
				save(t, s, v)
				loaded := inventory(t, s)
				id := report.NodeIDs["peer"]
				if loaded.Nodes[id].PasswordPromptPattern != `(?i)password for .*:` {
					t.Fatalf("password prompt pattern not persisted: %q", loaded.Nodes[id].PasswordPromptPattern)
				}
				cloned := loaded.Clone()
				*cloned.Nodes[id].Execution.Login = true
				if *loaded.Nodes[id].Execution.Login {
					t.Fatal("inventory clone shares execution login")
				}
				b, err := s.Export(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				data, err := archive.Encode(t.Context(), b, vault)
				if err != nil {
					t.Fatal(err)
				}
				b, err = archive.Decode(t.Context(), data, vault)
				if err != nil {
					t.Fatal(err)
				}
				dst, _ := open(t, target)
				if err := dst.Restore(t.Context(), b); err != nil {
					t.Fatal(err)
				}
				restoredNode := inventory(t, dst).Nodes[id]
				got := restoredNode.Execution
				if got == nil || got.Interpreter != ssh.InterpreterZsh || got.LaunchDialect != ssh.LaunchPOSIX || got.Login == nil || *got.Login {
					t.Fatalf("execution configuration lost: %+v", got)
				}
				if restoredNode.PasswordPromptPattern != `(?i)password for .*:` {
					t.Fatalf("password prompt pattern lost after restore: %q", restoredNode.PasswordPromptPattern)
				}
			})
		}
	}
}
