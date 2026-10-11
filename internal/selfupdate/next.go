package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// NextProgram picks the program to run when Mediarium restarts itself in
// place (after an update, a restore or the Restart button), with the same
// rules as the container's entrypoint: the installed update when it is there,
// is executable, matches its checksum and hasn't failed to start
// MaxBootAttempts times in a row (its start is counted); the program in the
// image otherwise. imageBin is "" outside the image, where the running
// program itself is the answer.
func NextProgram(dir, imageBin string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if imageBin == "" {
		return self, nil
	}
	pushed := filepath.Join(dir, FileApp)
	st, err := os.Stat(pushed)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return imageBin, nil
	}
	if want := strings.Fields(readLine(filepath.Join(dir, FileSum))); len(want) > 0 {
		have, err := fileSHA256(pushed)
		if err != nil || !strings.EqualFold(have, want[0]) {
			return imageBin, nil
		}
	}
	count := BootCount(dir)
	if count >= MaxBootAttempts {
		return imageBin, nil // the entrypoint puts it aside on the next container start
	}
	if err := os.WriteFile(filepath.Join(dir, FileBootCount), []byte(fmt.Sprintf("%d\n", count+1)), 0o644); err != nil {
		return imageBin, nil // without the count a bad update could not be undone
	}
	return pushed, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
