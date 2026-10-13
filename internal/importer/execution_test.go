package importer_test

import (
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/importer"
	"testing"
)

func TestCLIExecutionInheritance(t *testing.T) {
	const input = `schema_version: 2
execution:
  interpreter: bash
  launch_dialect: posix
  login: true
nodes:
  inherited:
    host_ref: host
    identity_ref: login
  override:
    host_ref: host
    identity_ref: login
    execution:
      interpreter: zsh
      login: false
  passthrough:
    host_ref: host
    identity_ref: login
    execution:
      interpreter: server
`
	doc, _, err := importer.Decode([]byte(input), "xops-cli")
	if err != nil {
		t.Fatal(err)
	}
	inherited, override, pass := doc.Nodes["inherited"].Execution, doc.Nodes["override"].Execution, doc.Nodes["passthrough"].Execution
	if inherited.Interpreter != ssh.InterpreterBash || inherited.Login == nil || !*inherited.Login {
		t.Fatal("global execution not inherited")
	}
	if override.Interpreter != ssh.InterpreterZsh || override.Login == nil || *override.Login || override.LaunchDialect != ssh.LaunchPOSIX {
		t.Fatal("node execution override lost")
	}
	if pass.Interpreter != ssh.InterpreterServer || pass.Login != nil {
		t.Fatal("server inherited incompatible login")
	}
	*override.Login = true
	if !*inherited.Login {
		t.Fatal("execution values share state")
	}
}

func TestImportRejectsInvalidExecution(t *testing.T) {
	for _, format := range []string{"xops-cli", "server"} {
		header := "schema_version: 2\n"
		if format == "server" {
			header = "version: 1\n"
		}
		for _, execution := range []string{"{interpreter: arbitrary}", "{interpreter: sh, login: true}", "{interpreter: cmd, launch_dialect: posix}", "{interpreter: bash, unexpected: true}"} {
			if _, _, err := importer.Decode([]byte(header+"nodes:\n  peer:\n    execution: "+execution+"\n"), format); err == nil {
				t.Fatalf("accepted invalid %s execution %s", format, execution)
			}
		}
	}
}
