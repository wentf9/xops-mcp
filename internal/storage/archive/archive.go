// Package archive validates and encrypts offline, backend-neutral database copies.
package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
)

const MaxSize = 256 << 20
const envelopeContext = "xops-mcp database backup v3"

var magic = []byte("XOPSDB\x03")

func sourceKey(s storage.Source) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%s", s.Token, s.NodeID, s.Host, s.Port, s.User, s.Purpose)
}

// Validate authenticates every historical credential, reconstructs current
// ciphertext from immutable versions, and verifies executable source bindings.
func Validate(ctx context.Context, b *storage.Backup, vault *secure.Vault) error {
	if b.Format != storage.BackupFormat || b.Inventory.DomainID == "" || b.Inventory.Revision > 1<<63-1 {
		return errors.New("unsupported or invalid database archive")
	}
	plain, err := vault.Open("deployment:"+b.Inventory.DomainID, b.KeyCheck)
	if err != nil {
		return errors.New("archive master key does not match deployment")
	}
	ok := string(plain) == "xops-mcp master key v1"
	clear(plain)
	if !ok {
		return errors.New("invalid archive key check")
	}
	versions := map[[2]string]storage.CredentialVersion{}
	for _, v := range b.Versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := [2]string{v.ID, v.Version}
		if _, ok := versions[key]; ok || v.ID == "" || v.Version == "" {
			return errors.New("invalid or duplicate archived credential version")
		}
		material, err := vault.Decrypt(b.Inventory.DomainID, v.ID, v.Kind, v.Version, v.Ciphertext)
		if err != nil {
			return errors.New("archived credential cannot be decrypted")
		}
		err = secure.ValidateMaterial(v.Kind, material)
		material.Clear()
		if err != nil {
			return errors.New("archived credential material is invalid")
		}
		versions[key] = v
	}
	b.Inventory = b.Inventory.Clone()
	for id, c := range b.Inventory.Credentials {
		v, ok := versions[[2]string{c.ID, c.Version}]
		if !ok || v.Kind != c.Kind || len(c.Ciphertext) > 0 && !bytes.Equal(c.Ciphertext, v.Ciphertext) {
			return errors.New("current credential differs from archived history")
		}
		c.Ciphertext = bytes.Clone(v.Ciphertext)
		b.Inventory.Credentials[id] = c
	}
	_, current, err := xops.Snapshot(b.Inventory)
	if err != nil {
		return fmt.Errorf("invalid archived inventory: %w", err)
	}
	sources := map[string]storage.Source{}
	for _, s := range b.Sources {
		key := sourceKey(s)
		if _, ok := sources[key]; ok {
			return errors.New("duplicate archived source binding")
		}
		if s.CredentialID != "" {
			if _, ok := versions[[2]string{s.CredentialID, s.Version}]; !ok {
				return errors.New("archived source references missing credential version")
			}
		}
		sources[key] = s
	}
	for _, s := range current {
		if sources[sourceKey(s)] != s {
			return errors.New("archived current source binding is missing or changed")
		}
	}
	slices.SortFunc(b.Versions, func(a, c storage.CredentialVersion) int {
		if a.ID != c.ID {
			return strings.Compare(a.ID, c.ID)
		}
		return strings.Compare(a.Version, c.Version)
	})
	slices.SortFunc(b.Sources, func(a, c storage.Source) int { return strings.Compare(sourceKey(a), sourceKey(c)) })
	slices.SortFunc(b.Audit, func(a, c storage.AuditRecord) int {
		if a.ID < c.ID {
			return -1
		}
		if a.ID > c.ID {
			return 1
		}
		return 0
	})
	var last int64
	for _, a := range b.Audit {
		if a.ID <= last || a.ID == 1<<63-1 {
			return errors.New("invalid or duplicate archived audit ID")
		}
		last = a.ID
		if a.Event.Command != "" || len(a.Event.Paths) > 0 || a.Event.Error != "" || a.Event.Details != "" {
			return errors.New("archived audit contains unredacted fields")
		}
	}
	if b.Admin != nil && (b.Admin.Username == "" || b.Admin.Version == "" || len(b.Admin.PasswordHash) == 0) {
		return errors.New("invalid archived administrator")
	}
	// Empty collections have one representation for hashing and database exports.
	if b.MCPTokens == nil {
		b.MCPTokens = []storage.MCPTokenRecord{}
	}
	ids, digests := map[string]bool{}, map[string]bool{}
	for i := range b.MCPTokens {
		record := &b.MCPTokens[i]
		// Unlike backward-compatible token creation, v3 archives must state
		// their scope explicitly. Missing permissions must never become all.
		if record.Token.NodeScope == "" {
			return storage.ErrInvalidMCPTokenScope
		}
		if err := record.Token.NormalizeNodeScope(); err != nil {
			return err
		}
		if err := record.Validate(); err != nil {
			return err
		}
		if ids[record.Token.ID] || digests[record.Digest] {
			return errors.New("duplicate archived MCP token")
		}
		ids[record.Token.ID], digests[record.Digest] = true, true
	}
	slices.SortFunc(b.MCPTokens, func(a, b storage.MCPTokenRecord) int { return strings.Compare(a.Token.ID, b.Token.ID) })
	for id, n := range b.Inventory.Nodes {
		slices.Sort(n.Aliases)
		slices.Sort(n.TagIDs)
		b.Inventory.Nodes[id] = n
	}
	return ctx.Err()
}

func Encode(ctx context.Context, b storage.Backup, vault *secure.Vault) ([]byte, error) {
	if err := Validate(ctx, &b, vault); err != nil {
		return nil, err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	if len(data) > MaxSize-128 {
		return nil, errors.New("database archive exceeds 256 MiB limit; use native database backup")
	}
	return append(bytes.Clone(magic), vault.Seal(envelopeContext, data)...), nil
}
func Decode(ctx context.Context, data []byte, vault *secure.Vault) (storage.Backup, error) {
	var b storage.Backup
	if len(data) > MaxSize || !bytes.HasPrefix(data, magic) {
		return b, errors.New("unsupported database archive")
	}
	plain, err := vault.Open(envelopeContext, data[len(magic):])
	if err != nil {
		return b, errors.New("archive authentication failed; wrong key or damaged archive")
	}
	defer clear(plain)
	if err := json.Unmarshal(plain, &b); err != nil {
		return b, errors.New("invalid database archive payload")
	}
	if err := Validate(ctx, &b, vault); err != nil {
		return b, err
	}
	return b, nil
}
func Digest(ctx context.Context, b storage.Backup, vault *secure.Vault) (string, error) {
	if err := Validate(ctx, &b, vault); err != nil {
		return "", err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	defer clear(data)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
