package service_test

import (
	"errors"
	"testing"

	"github.com/wentf9/xops-mcp/internal/naming"
	"github.com/wentf9/xops-mcp/internal/service"
	"github.com/wentf9/xops-mcp/internal/storage"
	"github.com/wentf9/xops-mcp/internal/testutil"
)

func TestLoadedInventoryDoesNotExemptInvalidNames(t *testing.T) {
	s, _, _ := testutil.Store(t)
	v, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Seed a previously persisted invalid value below the business layer.
	v.Hosts["host"] = storage.Host{ID: "host", Name: " old name ", Address: "127.0.0.1", Port: 22}
	v.Revision++
	if err := s.Save(t.Context(), 0, v, nil); err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(t.Context(), s, nil)
	if svc != nil {
		defer testutil.Close(t, svc)
	}
	if !errors.Is(err, naming.ErrInvalid) {
		t.Fatalf("loaded invalid name was not rejected: %v", err)
	}
}
