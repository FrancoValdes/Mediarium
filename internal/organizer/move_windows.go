//go:build windows

package organizer

import (
	"errors"
	"syscall"
)

// errNotSameDevice is ERROR_NOT_SAME_DEVICE: the two places are on different drives.
const errNotSameDevice = syscall.Errno(17)

// isCrossDevice reports whether a rename failed because the two places are on
// different drives.
func isCrossDevice(err error) bool { return errors.Is(err, errNotSameDevice) }
