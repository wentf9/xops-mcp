package server_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-cli/core/testutil/sshfixture"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
	cryptoSSH "golang.org/x/crypto/ssh"
)

func TestIndependentHostKeyEnrollmentAndJumpRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	keys := mixedHostKeys(t, "ed25519")
	peer, err := sshfixture.NewWithOptions(ctx, sshfixture.Options{HostKeys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, peer)
	p := newAdminPeer(t, ctx, adminConfig(t))
	p.initialize()
	// Start with an unreachable endpoint: key enrollment and rotation must
	// work without discovering the host through a direct SSH connection.
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, closedPort, err := net.SplitHostPort(listener.Addr().String())
	testutil.Close(t, listener)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(closedPort)
	if err != nil {
		t.Fatal(err)
	}
	host := service.HostInput{Name: "private-target", Address: "127.0.0.1", Port: port}
	hostID := p.save("hosts", host)
	otherID := p.save("hosts", service.HostInput{Name: "another-host", Address: "127.0.0.1", Port: port})
	path := "/api/v1/hosts/" + hostID
	p.request("POST", path+"/probe", map[string]any{}, 502, nil)
	public := string(cryptoSSH.MarshalAuthorizedKey(peer.HostKey))
	preview := func(id, public string) string {
		t.Helper()
		data := p.request("POST", "/api/v1/hosts/"+id+"/key-preview", map[string]string{"hostKey": public}, 200, nil)
		var result struct {
			ProbeID, HostKey, Fingerprint string
			ExpiresIn                     int
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		key, _, _, _, err := cryptoSSH.ParseAuthorizedKey([]byte(public))
		if err != nil {
			t.Fatal(err)
		}
		if result.ProbeID == "" || result.HostKey != string(cryptoSSH.MarshalAuthorizedKey(key)) || result.Fingerprint != cryptoSSH.FingerprintSHA256(key) || result.ExpiresIn != 120 {
			t.Fatalf("invalid key confirmation preview: %s", data)
		}
		return result.ProbeID
	}
	confirm := func(id, token string, status int) {
		t.Helper()
		p.request("POST", "/api/v1/hosts/"+id+"/trust", map[string]string{"probeID": token}, status, nil)
	}
	certificate := &cryptoSSH.Certificate{Key: peer.HostKey, CertType: cryptoSSH.HostCert, ValidBefore: cryptoSSH.CertTimeInfinity}
	if err := certificate.SignCert(rand.Reader, keys[0]); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "ssh-ed25519 not-a-key", public + public, "no-pty " + public, string(cryptoSSH.MarshalAuthorizedKey(certificate))} {
		p.request("POST", path+"/key-preview", map[string]string{"hostKey": invalid}, 422, nil)
	}
	before := p.etag
	token := preview(hostID, strings.TrimSpace(public)+" independently-verified\n")
	v, err := p.app.Host.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Hosts[hostID].HostKey != "" || p.etag != before {
		t.Fatal("preview changed host trust before confirmation")
	}
	confirm(otherID, token, 409)
	token = preview(hostID, public)
	// Another authenticated session cannot consume this session's preview.
	jar, csrf := p.client.Jar, p.csrf
	p.client.Jar, err = cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	p.login(adminPassword)
	confirm(hostID, token, 409)
	p.client.Jar, p.csrf = jar, csrf
	confirm(hostID, token, 200)
	confirm(hostID, token, 409)
	stale := preview(hostID, public)
	p.request("PUT", path, host, 200, nil)
	confirm(hostID, stale, 412)
	p.request("POST", path+"/key-preview", map[string]string{"hostKey": public}, 412, func(r *http.Request) { r.Header.Set("If-Match", before) })
	// Rotate offline, then make the fixture reachable to check the actual
	// shared SSH connector with the configured jump chain and pinned key.
	rotated := string(cryptoSSH.MarshalAuthorizedKey(keys[1].PublicKey()))
	confirm(hostID, preview(hostID, rotated), 200)
	address, portText, err := net.SplitHostPort(peer.Address)
	if err != nil {
		t.Fatal(err)
	}
	port, err = strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	host.Address, host.Port = address, port
	p.request("PUT", path, host, 200, nil)
	jumpID := p.save("hosts", service.HostInput{Name: "jump", Address: address, Port: port})
	confirm(jumpID, preview(jumpID, public), 200)
	credentialID := p.save("credentials", service.CredentialInput{Name: "fixture-password", Kind: "password", Secret: &service.MaterialInput{Password: sshfixture.Password}})
	identityID := p.save("identities", service.IdentityInput{Name: "fixture", User: "fixture", CredentialID: credentialID})
	jumpNodeID := p.save("nodes", service.NodeInput{Name: "jump", HostID: jumpID, IdentityID: identityID, SudoMode: "none"})
	nodeID := p.save("nodes", service.NodeInput{Name: "target", HostID: hostID, IdentityID: identityID, JumpIDs: []string{jumpNodeID}, SudoMode: "none"})
	p.request("POST", "/api/v1/nodes/"+nodeID+"/test", map[string]any{}, 200, nil)
	if peer.Forwards.Load() == 0 || peer.Executed.Load() != 0 {
		t.Fatal("connection test bypassed the jump or executed a command")
	}
	view, err := p.app.Host.Service.Coordinator.Resolve(ctx, ports.ResolveRequest{Selectors: []string{nodeID}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ports.Bind(view, "test", "xops_ssh_run", map[string]string{"command": "hostname"})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := p.app.Host.Service.Coordinator.Enter(ctx, ports.Admission{OperationID: "before-key-rotation", Phase: ports.Execute, Snapshot: view, Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, permit)
	wrong := string(cryptoSSH.MarshalAuthorizedKey(mixedHostKeys(t, "ed25519")[0].PublicKey()))
	confirm(hostID, preview(hostID, wrong), 200)
	if permit.Context().Err() == nil {
		t.Fatal("key rotation did not revoke the prior admission")
	}
	p.request("POST", "/api/v1/nodes/"+nodeID+"/test", map[string]any{}, 502, nil)
	confirm(hostID, preview(hostID, public), 200)
	p.request("POST", "/api/v1/nodes/"+nodeID+"/test", map[string]any{}, 200, nil)
	audit := p.request("GET", "/api/v1/audit", nil, 200, nil)
	if !strings.Contains(string(audit), "admin.host.trust") {
		t.Fatal("independently provided key confirmation was not audited")
	}
}
