//go:build !windows

package main

import (
	"log"
	"os"
	"syscall"

	"github.com/rdborg/mediarium/internal/selfupdate"
)

// restartInPlace replaces this process with the next program (the installed
// update or the one in the image, see selfupdate.NextProgram). It returns
// only if that fails, and the process then exits as it always did.
func restartInPlace(updateDir string) {
	next, err := selfupdate.NextProgram(updateDir, os.Getenv("MEDIARIUM_IMAGE_BIN"))
	if err != nil {
		log.Printf("Mediarium could not restart in place, so it exits to be started again: %v", err)
		return
	}
	log.Printf("Mediarium is starting again in place: %s", next)
	if err := syscall.Exec(next, append([]string{next}, os.Args[1:]...), os.Environ()); err != nil {
		log.Printf("Mediarium could not restart in place, so it exits to be started again: %v", err)
	}
}
