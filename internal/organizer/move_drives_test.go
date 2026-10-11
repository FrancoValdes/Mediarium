//go:build !windows

package organizer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A move between two real drives: a rename fails with "cross-device", and the
// copy takes over by itself. The second drive is the memory-backed /dev/shm of
// a Linux container; where there is none, or it shares the temporary folder's
// drive, the test is skipped.
func TestMoveBetweenTwoRealDrives(t *testing.T) {
	shm := "/dev/shm"
	if info, err := os.Stat(shm); err != nil || !info.IsDir() {
		t.Skip("no second filesystem to move to")
	}
	work := t.TempDir()
	same, supported, err := SameFilesystem(work, shm)
	if err != nil || !supported || same {
		t.Skip("/dev/shm is on the same filesystem as the temporary folder")
	}
	other, err := os.MkdirTemp(shm, "mediarium-move-test-*")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })

	src := movieFolder(t, filepath.Join(work, "movies"))
	before := tree(t, src)
	dst := filepath.Join(other, "Heat (1995)")
	mv, err := StartMove(context.Background(), []MoveUnit{{Src: src, Dst: dst, Dir: true}}, MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tree(t, dst) != before {
		t.Fatalf("the copy differs:\n%s", tree(t, dst))
	}
	if gone(src) {
		t.Fatal("the original went before Commit")
	}
	mv.Commit()
	if !gone(src) {
		t.Fatal("the original is still there after Commit")
	}

	// One file, too.
	f := filepath.Join(work, "x.mkv")
	write(t, f, "x")
	mv, err = StartMove(context.Background(), []MoveUnit{{Src: f, Dst: filepath.Join(other, "x.mkv")}}, MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mv.Commit()
	if read(t, filepath.Join(other, "x.mkv")) != "x" || !gone(f) {
		t.Fatal("a single file did not move between drives")
	}
}
