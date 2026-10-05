// Package command implements the server's deployment and offline operations.
package command

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mcpruntime "github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-mcp/internal/adminauth"
	"github.com/wentf9/xops-mcp/internal/config"
	"github.com/wentf9/xops-mcp/internal/importer"
	"github.com/wentf9/xops-mcp/internal/server"
)

const help = `xops-mcp: standalone HTTP MCP server

Commands:
  keygen --out FILE                         Create a private 256-bit key or MCP token
  migrate --config FILE                     Initialize or migrate the deployment
  serve --config FILE                       Serve authenticated HTTP MCP
  status --config FILE                      Inspect the offline deployment revision
  admin-init --config FILE --password-file FILE [--username admin]
  admin-reset --config FILE --password-file FILE
         [--password-stdin instead of --password-file]
                                            Initialize/reset the single administrator
  import --config FILE --file FILE           Preview a one-way inventory import
         [--format server|xops-cli] [--include-secrets] [--replace]
         [--apply --expected-revision N]     Apply the reviewed revision atomically
  recover --config FILE [--id ID]            List offline transfer evidence
          [--verify] [--cleanup] [--resolve-unknown --reason TEXT]

Offline commands require the service to be stopped. Import previews do not
change business data. Node IDs and credentials are never loaded from personal
CLI/OpenSSH configuration. See docs/server.md for deployment and import formats.
`

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (retErr error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := io.WriteString(stdout, help)
		return err
	}
	if args[0] == "keygen" {
		return keygen(args[1:], stderr)
	}
	command := args[0]
	switch command {
	case "serve", "migrate", "status", "import", "recover", "admin-init", "admin-reset":
	default:
		return errors.New("unknown command; use xops-mcp help")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "deployment configuration file")
	var file, format, expected, id, reason string
	var apply, dryRun, includeSecrets, replace, verify, cleanup, resolve bool
	var username, passwordFile string
	var passwordStdin bool
	if command == "admin-init" || command == "admin-reset" {
		flags.StringVar(&passwordFile, "password-file", "", "private file containing the administrator password")
		flags.BoolVar(&passwordStdin, "password-stdin", false, "read the administrator password from stdin")
		if command == "admin-init" {
			flags.StringVar(&username, "username", "admin", "administrator username")
		}
	}
	if command == "import" {
		flags.StringVar(&file, "file", "", "inventory file or - for stdin")
		flags.StringVar(&format, "format", "server", "server or xops-cli")
		flags.StringVar(&expected, "expected-revision", "", "revision reported by preview")
		flags.BoolVar(&apply, "apply", false, "commit the import")
		flags.BoolVar(&dryRun, "dry-run", false, "preview only (default)")
		flags.BoolVar(&includeSecrets, "include-secrets", false, "authorize supplied credential material")
		flags.BoolVar(&replace, "replace", false, "remove nodes, hosts and identities absent from input")
	}
	if command == "recover" {
		flags.StringVar(&id, "id", "", "transfer ID")
		flags.StringVar(&reason, "reason", "", "operator reason")
		flags.BoolVar(&verify, "verify", false, "verify original remote destination")
		flags.BoolVar(&cleanup, "cleanup", false, "clean up original remote temporary file")
		flags.BoolVar(&resolve, "resolve-unknown", false, "acknowledge uncertain result without repeating upload")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *configPath == "" {
		return errors.New("--config is required; positional arguments are not accepted")
	}
	if command == "import" && (file == "" || apply && dryRun || apply && expected == "") {
		return errors.New("import requires --file; --apply requires --expected-revision and excludes --dry-run")
	}
	if (command == "admin-init" || command == "admin-reset") && (passwordFile == "") == !passwordStdin {
		return errors.New("select exactly one of --password-file or --password-stdin")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if command == "serve" {
		return server.Run(ctx, cfg, stderr)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	openHost := server.OpenExistingHost
	if command == "migrate" {
		openHost = server.OpenHost
	}
	host, err := openHost(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, host.Close()) }()
	encode := func(value any) error { return json.NewEncoder(stdout).Encode(value) }
	if command == "admin-init" || command == "admin-reset" {
		var data []byte
		if passwordStdin {
			data, err = readStdin(ctx, stdin, 75)
		} else {
			data, err = config.ReadFile(passwordFile, 74, true)
		}
		if err != nil {
			return err
		}
		defer clear(data)
		password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		manager, err := adminauth.New(host.Store, 0)
		if err != nil {
			return err
		}
		if command == "admin-init" {
			err = manager.Initialize(ctx, username, password)
		} else {
			err = manager.ResetPassword(ctx, password)
		}
		if err != nil {
			return err
		}
		return encode(map[string]bool{"administrator_updated": true, "sessions_revoked": true})
	}
	if command == "recover" {
		entries, err := mcpruntime.RecoverTransfers(ctx, mcpruntime.RecoveryOptions{StateDir: filepath.Join(cfg.DataDir, "transfers"), TransferID: id, Verify: verify, Cleanup: cleanup, ResolveUnknown: resolve, Reason: reason, MaxRecords: 4096}, mcpruntime.WithDependencies(host.Dependencies()))
		return errors.Join(err, encode(entries))
	}
	base, err := host.Store.Load(ctx)
	if err != nil {
		return err
	}
	if command != "import" {
		return encode(struct {
			DomainID string `json:"domain_id"`
			Revision uint64 `json:"revision"`
			Nodes    int    `json:"nodes"`
		}{base.DomainID, base.Revision, len(base.Nodes)})
	}
	var data []byte
	if file == "-" {
		data, err = readStdin(ctx, stdin, (4<<20)+1)
	} else {
		data, err = config.ReadFile(file, 4<<20, includeSecrets)
	}
	if err != nil {
		return err
	}
	defer clear(data)
	doc, warnings, err := importer.Decode(data, format)
	if err != nil {
		return err
	}
	candidate, report, err := importer.Plan(base, doc, host.Vault, importer.Options{IncludeSecrets: includeSecrets, Replace: replace})
	report.Warnings = append(warnings, report.Warnings...)
	if err != nil {
		return errors.Join(err, encode(report))
	}
	if !apply {
		return encode(report)
	}
	revision, err := strconv.ParseUint(expected, 10, 63)
	if err != nil {
		return errors.New("expected revision must be a nonnegative integer")
	}
	current, err := host.Service.Apply(ctx, revision, candidate)
	if err != nil {
		return err
	}
	return encode(struct {
		Report   importer.Report `json:"import"`
		Revision uint64          `json:"revision"`
	}{report, current})
}

func keygen(args []string, stderr io.Writer) (retErr error) {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("out", "", "new private file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("keygen requires --out FILE")
	}
	file, err := os.OpenFile(*path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create secret file: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	var key [32]byte
	rand.Read(key[:])
	defer clear(key[:])
	data := make([]byte, 65)
	hex.Encode(data, key[:])
	data[64] = '\n'
	defer clear(data)
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write secret file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync secret file: %w", err)
	}
	return nil
}
