//go:build unix

package command

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Poll readiness instead of placing a blocking read on a detached goroutine.
// This also handles inherited blocking stdin descriptors that do not support
// os.File deadlines. Command input has one reader; no other goroutine may
// consume this descriptor between readiness and Read.
type polledInput struct {
	ctx  context.Context
	file *os.File
	fd   int32
}

func (r polledInput) Read(p []byte) (int, error) {
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		fds := []unix.PollFd{{Fd: r.fd, Events: unix.POLLIN}}
		_, err := unix.Poll(fds, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if fds[0].Revents&unix.POLLNVAL != 0 {
			return 0, os.ErrClosed
		}
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0 {
			return r.file.Read(p)
		}
	}
}

func readFileInput(ctx context.Context, file *os.File, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(polledInput{ctx, file, int32(file.Fd())}, limit))
}
