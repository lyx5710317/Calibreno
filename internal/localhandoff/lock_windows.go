package localhandoff

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLockPacket(file *os.File) (func(), error) {
	handle := windows.Handle(file.Fd())
	var offset windows.Overlapped
	err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, &offset) }, nil
}
