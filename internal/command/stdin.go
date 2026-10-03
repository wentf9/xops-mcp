package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

type contextInput struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextInput) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// OS stdin is borrowed and remains open. Other streaming inputs must implement
// ReadCloser with Close interrupting Read; ownership is consumed on cancellation.
// In-memory readers need no cancellation worker. Arbitrary blocking Readers are
// rejected because Go cannot cancel their Read method safely.
func readStdin(ctx context.Context, input io.Reader, limit int64) (data []byte, retErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(retErr, ctxErr) {
			retErr = errors.Join(retErr, ctxErr)
		}
		if retErr != nil {
			clear(data)
			data = nil
		}
	}()
	if file, ok := input.(*os.File); ok {
		return readFileInput(ctx, file, limit)
	}
	switch input.(type) {
	case *bytes.Buffer, *bytes.Reader, *strings.Reader:
		return io.ReadAll(io.LimitReader(contextInput{ctx, input}, limit))
	}
	closer, ok := input.(io.ReadCloser)
	if !ok {
		return nil, errors.New("stdin requires a file, in-memory reader, or reader whose Close interrupts Read")
	}
	closed := make(chan error, 1)
	// Cancellation closes the owned streaming reader, unblocking the synchronous
	// Read below. Stop or join this callback before returning; no read is detached.
	stop := context.AfterFunc(ctx, func() { closed <- closer.Close() })
	defer func() {
		if !stop() {
			retErr = errors.Join(retErr, <-closed)
		}
	}()
	return io.ReadAll(io.LimitReader(contextInput{ctx, input}, limit))
}
