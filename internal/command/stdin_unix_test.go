//go:build unix

package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPipeStdinDeadlineKeepsBorrowedFileUsable(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}()
	// readStdin uses readiness polling without requiring a Go file deadline,
	// including for inherited descriptors used by process stdin.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	data, err := readStdin(ctx, reader, 1024)
	if !errors.Is(err, context.DeadlineExceeded) || len(data) != 0 {
		t.Fatalf("stdin deadline = %q %v", data, err)
	}
	if _, err := writer.Write([]byte("available")); err != nil {
		t.Fatal(err)
	}
	data, err = readStdin(t.Context(), reader, 9)
	if err != nil || !bytes.Equal(data, []byte("available")) {
		t.Fatalf("borrowed stdin changed or closed: %q %v", data, err)
	}
}
