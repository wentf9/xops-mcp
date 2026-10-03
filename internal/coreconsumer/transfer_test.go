package coreconsumer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wentf9/xops-cli/core/mcp/runtime"
	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	cryptoSSH "golang.org/x/crypto/ssh"
)

type fixtureSecrets struct{}

func (fixtureSecrets) ResolveSecret(ctx context.Context, _ ssh.SecretRequest) ([]byte, error) {
	return []byte(sshfixture.Password), ctx.Err()
}

type fixtureTrust struct{ key cryptoSSH.PublicKey }

func (v fixtureTrust) Verify(ctx context.Context, _ ssh.HostKeyRequest, key cryptoSSH.PublicKey) error {
	if !bytes.Equal(v.key.Marshal(), key.Marshal()) {
		return errors.New("fixture host key mismatch")
	}
	return ctx.Err()
}

func callTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, input, output any) {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("%s failed: %+v", name, result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, output); err != nil {
		t.Fatal(err)
	}
}

func exerciseCoreTools(t *testing.T, ctx context.Context, session *mcp.ClientSession, client *http.Client) {
	t.Helper()
	var result map[string]any
	callTool(t, ctx, session, "xops_ssh_run", map[string]any{"nodeID": "compat-node", "command": "hostname"}, &result)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("fixture-output")) {
		t.Fatalf("command result = %s", data)
	}
	payload := []byte("consumer file\x00\xff\n")
	sum := sha256.Sum256(payload)
	var upload runtime.PreparedTransferOutput
	callTool(t, ctx, session, "xops_prepare_upload", runtime.PrepareUploadInput{RequestID: "upload", NodeID: "compat-node", RemotePath: "/consumer", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}, &upload)
	sendContent(t, ctx, client, upload, bytes.NewReader(payload))
	var download runtime.PreparedTransferOutput
	callTool(t, ctx, session, "xops_prepare_download", runtime.PrepareDownloadInput{RequestID: "download", NodeID: "compat-node", RemotePath: "/consumer"}, &download)
	got := sendContent(t, ctx, client, download, nil)
	if !bytes.Equal(got, payload) {
		t.Fatalf("file content changed: %q", got)
	}
}

func sendContent(t *testing.T, ctx context.Context, client *http.Client, prepared runtime.PreparedTransferOutput, body io.Reader) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, prepared.Method, prepared.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range prepared.Headers {
		req.Header.Set(name, value)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResource(t, response.Body)
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("content HTTP %d: %s", response.StatusCode, data)
	}
	return data
}
