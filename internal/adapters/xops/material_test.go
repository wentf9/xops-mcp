package xops_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestSecretSourceBindsEndpointVersionAndPurpose(t *testing.T) {
	s, vault, _ := testutil.Store(t)
	svc, err := service.New(t.Context(), s, func(ctx context.Context, _ []ssh.ConnectionPlan) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, svc)
	doc := testutil.Document(t)
	v, report := testutil.Plan(t, s, vault, doc)
	if _, err := svc.Apply(t.Context(), 0, v); err != nil {
		t.Fatal(err)
	}
	hop := svc.Coordinator.Snapshot().Targets[report.NodeIDs["peer"]].Plan.Hops[0]
	m := &xops.Materials{Store: s, Vault: vault, DomainID: v.DomainID}
	request := ssh.SecretRequest{Kind: ssh.SecretKindLoginPassword, NodeID: hop.NodeID, Host: hop.Address, Port: hop.Port, User: hop.User, VersionToken: hop.AuthUpdateToken}
	password, err := m.ResolveSecret(t.Context(), request)
	if err != nil || string(password) != doc.Credentials["login"].Password {
		t.Fatalf("bound material: %v", err)
	}
	clear(password)
	for _, change := range []func(*ssh.SecretRequest){func(r *ssh.SecretRequest) { r.Host = "127.0.0.99" }, func(r *ssh.SecretRequest) { r.Port++ }, func(r *ssh.SecretRequest) { r.User = "other" }, func(r *ssh.SecretRequest) { r.NodeID = "other" }, func(r *ssh.SecretRequest) { r.Kind = ssh.SecretKindSuPassword }, func(r *ssh.SecretRequest) { r.VersionToken = "other" }} {
		bad := request
		change(&bad)
		if secret, err := m.ResolveSecret(t.Context(), bad); !errors.Is(err, ssh.ErrSnapshotMismatch) {
			clear(secret)
			t.Fatalf("unbound source accepted: %v", err)
		}
	}
	v, err = s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for id, h := range v.Hosts {
		h.Address = "127.0.0.2"
		v.Hosts[id] = h
	}
	if _, err := svc.Apply(t.Context(), 1, v); err != nil {
		t.Fatal(err)
	}
	password, err = m.ResolveSecret(t.Context(), request)
	if err != nil {
		t.Fatalf("ordinary edit lost source for prior admitted operation: %v", err)
	}
	clear(password)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if secret, err := m.ResolveSecret(ctx, request); !errors.Is(err, context.Canceled) {
		clear(secret)
		t.Fatal("cancelled request resolved secret")
	}
}
