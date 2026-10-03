//go:build !unix

package command

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

func readFileInput(ctx context.Context, file *os.File, limit int64) (data []byte, retErr error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode().IsRegular() {
		return io.ReadAll(io.LimitReader(contextInput{ctx, file}, limit))
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("stdin requires a deadline")
	}
	if err := file.SetReadDeadline(deadline); err != nil {
		return nil, errors.New("stdin does not support interruptible reads on this platform; use --file with a regular file")
	}
	done := make(chan error, 1)
	// A read deadline interrupts the borrowed stream without closing it. Join
	// the cancellation callback before resetting the deadline.
	stop := context.AfterFunc(ctx, func() { done <- file.SetReadDeadline(time.Now()) })
	defer func() {
		if !stop() {
			retErr = errors.Join(retErr, <-done)
		}
		retErr = errors.Join(retErr, file.SetReadDeadline(time.Time{}))
	}()
	return io.ReadAll(io.LimitReader(contextInput{ctx, file}, limit))
}
