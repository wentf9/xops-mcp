package postgres

import (
	"bytes"
	"testing"

	"github.com/wentf9/xops-cli/core/mcp/ports"
	"github.com/wentf9/xops-mcp/internal/secure"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/storage/archive"
)

func TestBatchedRestoreRollsBackEarlierWrites(t *testing.T) {
	source, vault, _, _ := fixture(t)
	b := largeBackup(t, source, vault, 1200)
	target, _, _, _ := fixture(t)
	before, err := target.Export(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.db.ExecContext(t.Context(), "ALTER TABLE audit_events ADD CONSTRAINT reject_late CHECK (operation_id <> 'reject-late')"); err != nil {
		t.Fatal(err)
	}
	// The final audit record is rejected after earlier inventory, historical
	// credentials and audit records were written. None may survive rollback.
	b.Audit[len(b.Audit)-1].Event.OperationID = "reject-late"
	if err := target.Restore(t.Context(), b); err == nil {
		t.Fatal("late database failure committed")
	}
	after, err := target.Export(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want, err := archive.Digest(t.Context(), before, vault)
	if err != nil {
		t.Fatal(err)
	}
	got, err := archive.Digest(t.Context(), after, vault)
	if err != nil || got != want {
		t.Fatalf("partial restore survived rollback: %v", err)
	}
	if _, err := target.db.ExecContext(t.Context(), "ALTER TABLE audit_events DROP CONSTRAINT reject_late"); err != nil {
		t.Fatal(err)
	}
	if err := target.Restore(t.Context(), b); err != nil {
		t.Fatal("restore after rollback:", err)
	}
	after, err = target.Export(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want, err = archive.Digest(t.Context(), b, vault)
	if err != nil {
		t.Fatal(err)
	}
	got, err = archive.Digest(t.Context(), after, vault)
	if err != nil || got != want {
		t.Fatalf("restored history differs: %v", err)
	}
	if err := target.Append(t.Context(), ports.AuditEvent{OperationID: "after-restore"}); err != nil {
		t.Fatal(err)
	}
	records, err := target.Audit(t.Context(), storage.AuditQuery{Limit: 1})
	if err != nil || len(records) != 1 || records[0].ID <= b.Audit[len(b.Audit)-1].ID {
		t.Fatal("audit identity not restored", err)
	}
}

func TestClearingInventoryKeepsPermanentTombstones(t *testing.T) {
	s, vault, _, _ := fixture(t)
	b := largeBackup(t, s, vault, 5)
	if err := s.Save(t.Context(), 0, b.Inventory, b.Sources); err != nil {
		t.Fatal(err)
	}
	empty := b.Inventory.Clone()
	empty.Revision = 2
	empty.Nodes = nil
	empty.Deleted = nil
	if err := s.Save(t.Context(), 1, empty, nil); err != nil {
		t.Fatal(err)
	}
	v, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Nodes) != 0 || len(v.Deleted) != len(b.Inventory.Deleted)+len(b.Inventory.Nodes) {
		t.Fatal("clearing inventory dropped deletion history")
	}
	for id := range b.Inventory.Nodes {
		if !v.Deleted[id] {
			t.Fatal("missing deleted node tombstone")
		}
	}
	v.Revision = 3
	v.Nodes = b.Inventory.Nodes
	v.Deleted = nil
	if err := s.Save(t.Context(), 2, v, b.Sources); err == nil {
		t.Fatal("cleared node ID was reused")
	}
	actual, err := s.Load(t.Context())
	if err != nil || actual.Revision != 2 || len(actual.Nodes) != 0 {
		t.Fatal("failed reuse changed inventory", err)
	}
}

func TestLargeCredentialBatchPreservesCiphertext(t *testing.T) {
	s, vault, _, _ := fixture(t)
	b := largeBackup(t, s, vault, 32)
	// Valid password materials can be 64 KiB. A small number of rows already
	// requires splitting by payload size, independent of the inventory row limit.
	for i, version := range b.Versions {
		data, err := vault.Encrypt(b.Inventory.DomainID, version.ID, version.Kind, version.Version, secure.Material{Password: bytes.Repeat([]byte{byte(i)}, 64<<10)})
		if err != nil {
			t.Fatal(err)
		}
		b.Versions[i].Ciphertext = data
		c := b.Inventory.Credentials[version.ID]
		if c.Version == version.Version {
			c.Ciphertext = data
			b.Inventory.Credentials[version.ID] = c
		}
	}
	if err := s.Restore(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	actual, err := s.Export(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want, err := archive.Digest(t.Context(), b, vault)
	if err != nil {
		t.Fatal(err)
	}
	got, err := archive.Digest(t.Context(), actual, vault)
	if err != nil || got != want {
		t.Fatalf("large credential batch changed ciphertext: %v", err)
	}
}
