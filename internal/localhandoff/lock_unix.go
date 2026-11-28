//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package localhandoff

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockPacket(file *os.File) (func(), error) {
	fd := int(file.Fd())
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		if err != nil {
			return nil, err
		}
		return func() { _ = unix.Flock(fd, unix.LOCK_UN) }, nil
	}
}
