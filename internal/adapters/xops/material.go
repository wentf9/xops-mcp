package xops

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/wentf9/xops-cli/core/ssh"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	cryptoSSH "golang.org/x/crypto/ssh"
)

type Materials struct {
	Store    storage.Repository
	Vault    *secure.Vault
	DomainID string
}

var _ ssh.HostKeyAlgorithmSource = (*Materials)(nil)

func (m *Materials) pinnedHostKey(ctx context.Context, req ssh.HostKeyRequest) (cryptoSSH.PublicKey, error) {
	source, err := m.Store.Source(ctx, storage.Source{Token: req.VersionToken, NodeID: req.NodeID, Host: req.Host, Port: req.Port, User: req.User, Purpose: "trust"})
	if err != nil {
		return nil, errors.Join(ssh.ErrSnapshotMismatch, err)
	}
	return ParseHostKey(source.HostKey)
}

func (m *Materials) HostKeyAlgorithms(ctx context.Context, req ssh.HostKeyRequest) ([]string, error) {
	key, err := m.pinnedHostKey(ctx, req)
	if err != nil {
		return nil, err
	}
	// RSA public keys use rsa-sha2 signature algorithms; ssh-rsa would select
	// the obsolete SHA-1 signature rather than the pinned RSA key's safe modes.
	if key.Type() == cryptoSSH.KeyAlgoRSA {
		return []string{cryptoSSH.KeyAlgoRSASHA512, cryptoSSH.KeyAlgoRSASHA256}, nil
	}
	return []string{key.Type()}, nil
}

func (m *Materials) material(ctx context.Context, query storage.Source) (storage.Source, storage.Credential, secure.Material, error) {
	if err := ctx.Err(); err != nil {
		return storage.Source{}, storage.Credential{}, secure.Material{}, err
	}
	source, err := m.Store.Source(ctx, query)
	if err != nil {
		return source, storage.Credential{}, secure.Material{}, errors.Join(ssh.ErrSnapshotMismatch, err)
	}
	if source.CredentialID == "" {
		return source, storage.Credential{}, secure.Material{}, ssh.ErrInteractionRequired
	}
	c, err := m.Store.Credential(ctx, source.CredentialID, source.Version)
	if err != nil {
		return source, c, secure.Material{}, err
	}
	material, err := m.Vault.Decrypt(m.DomainID, c.ID, c.Kind, c.Version, c.Ciphertext)
	if err != nil {
		return source, c, secure.Material{}, fmt.Errorf("decrypt bound credential: %w", err)
	}
	if err := ctx.Err(); err != nil {
		material.Clear()
		return source, c, secure.Material{}, err
	}
	return source, c, material, nil
}

func (m *Materials) ResolveSecret(ctx context.Context, req ssh.SecretRequest) ([]byte, error) {
	purpose := "auth"
	switch req.Kind {
	case ssh.SecretKindLoginPassword, ssh.SecretKindPrivateKeyPassphrase:
	case ssh.SecretKindSuPassword, ssh.SecretKindSudoPassword:
		purpose = "privilege"
	default:
		return nil, errors.New("unsupported secret purpose")
	}
	_, c, material, err := m.material(ctx, storage.Source{Token: req.VersionToken, NodeID: req.NodeID, Host: req.Host, Port: req.Port, User: req.User, Purpose: purpose})
	if err != nil {
		return nil, err
	}
	defer material.Clear()
	if req.Kind == ssh.SecretKindPrivateKeyPassphrase && c.Kind == "key" {
		return bytes.Clone(material.Passphrase), nil
	}
	if c.Kind != "password" {
		return nil, errors.New("credential kind does not match secret purpose")
	}
	return bytes.Clone(material.Password), nil
}

type keyLease struct{ signer cryptoSSH.Signer }

func (k *keyLease) Signer() cryptoSSH.Signer { return k.signer }
func (k *keyLease) Close() error             { k.signer = nil; return nil }

func (m *Materials) OpenKey(ctx context.Context, req ssh.KeyRequest) (ssh.KeyLease, error) {
	_, c, material, err := m.material(ctx, storage.Source{Token: req.VersionToken, NodeID: req.NodeID, Host: req.Host, Port: req.Port, User: req.User, Purpose: "auth"})
	if err != nil {
		return nil, err
	}
	defer material.Clear()
	if c.Kind != "key" || c.ID != req.Reference || req.Path != "" {
		return nil, ssh.ErrSnapshotMismatch
	}
	var signer cryptoSSH.Signer
	if len(material.Passphrase) > 0 {
		signer, err = cryptoSSH.ParsePrivateKeyWithPassphrase(material.PrivateKey, material.Passphrase)
	} else {
		signer, err = cryptoSSH.ParsePrivateKey(material.PrivateKey)
	}
	if err != nil {
		return nil, errors.New("stored private key cannot be parsed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &keyLease{signer}, nil
}

func (m *Materials) Verify(ctx context.Context, req ssh.HostKeyRequest, key cryptoSSH.PublicKey) error {
	trusted, err := m.pinnedHostKey(ctx, req)
	if err != nil {
		return err
	}
	if !bytes.Equal(trusted.Marshal(), key.Marshal()) {
		return errors.New("SSH host key does not match the pinned deployment key")
	}
	return ctx.Err()
}
