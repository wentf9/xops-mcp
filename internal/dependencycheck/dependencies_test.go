package dependencycheck_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNoCLIPresentationDependencies(t *testing.T) {
	output := goList(t, "-deps", "-test", "./...")
	for dependency := range strings.FieldsSeq(string(output)) {
		for _, forbidden := range []string{
			"github.com/wentf9/xops-cli/cmd",
			"github.com/wentf9/xops-cli/pkg/tui",
			"github.com/spf13/cobra",
			"charm.land/bubbletea",
			"charm.land/huh",
		} {
			if dependency == forbidden || strings.HasPrefix(dependency, forbidden+"/") {
				t.Errorf("CLI presentation package entered the server dependency graph: %s", dependency)
			}
		}
	}
}

func TestUpstreamUsesPublishedModuleVersion(t *testing.T) {
	output := goList(t, "-m", "-json", "github.com/wentf9/xops-cli")
	var module struct {
		Path    string
		Version string
		Replace *json.RawMessage
	}
	if err := json.Unmarshal(output, &module); err != nil {
		t.Fatalf("decode upstream module: %v", err)
	}
	if module.Path != "github.com/wentf9/xops-cli" || module.Version == "" || module.Replace != nil {
		t.Fatalf("upstream must use a pinned remote module without replacement: %s", output)
	}
}

// Dependency listing does not type-check exported APIs. Compile the actual
// product with the declared pin even when this test runs in a local workspace,
// so a successful joint checkout cannot conceal an unpublished dependency.
func TestPublishedDependencyBuildsProduct(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", filepath.Join(t.TempDir(), "xops-mcp"), "./cmd/xops-mcp")
	command.Dir = "../.."
	command.Env = append(os.Environ(), "GOWORK=off")
	command.WaitDelay = time.Second
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build standalone product with the declared upstream version: %v\n%s", err, output)
	}
}

func TestOnlyCoreUpstreamDependencies(t *testing.T) {
	const upstream = "github.com/wentf9/xops-cli"
	for _, target := range []string{"linux", "windows", "darwin"} {
		t.Run(target, func(t *testing.T) {
			output := goListTarget(t, target, "-deps", "-test", "./...")
			for dependency := range strings.FieldsSeq(string(output)) {
				if dependency == upstream || (strings.HasPrefix(dependency, upstream+"/") && !strings.HasPrefix(dependency, upstream+"/core/")) {
					t.Errorf("application package entered the server dependency graph: %s", dependency)
				}
			}
		})
	}
}

func goList(t *testing.T, args ...string) []byte {
	t.Helper()
	return goListTarget(t, "", args...)
}

func goListTarget(t *testing.T, target string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", append([]string{"list"}, args...)...)
	command.Dir = "../.."
	command.Env = append(os.Environ(), "GOWORK=off")
	if target != "" {
		command.Env = append(command.Env, "GOOS="+target, "GOARCH=amd64", "CGO_ENABLED=0")
	}
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect Go dependency boundary: %v\n%s", err, output)
	}
	return output
}
