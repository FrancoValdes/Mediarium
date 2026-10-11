//go:build !windows

package organizer

import (
	"errors"
	"syscall"
)

// isCrossDevice reports whether a rename failed because the two places are on
// different drives (or different mounts of one drive).
func isCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }
