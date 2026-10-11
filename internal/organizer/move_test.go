package organizer

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rdborg/mediarium/internal/fileperms"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gone(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

// tree lists the files and folders under dir, relative.
func tree(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func movieFolder(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "Heat (1995)")
	write(t, filepath.Join(dir, "Heat (1995).mkv"), strings.Repeat("video", 1000))
	write(t, filepath.Join(dir, "Heat (1995).en.srt"), "subtitles")
	write(t, filepath.Join(dir, "movie.nfo"), "<movie/>")
	write(t, filepath.Join(dir, "extras", "Making of.mkv"), "extra")
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMoveFolderByRenameAndBack(t *testing.T) {
	work := t.TempDir()
	from, to := filepath.Join(work, "movies"), filepath.Join(work, "movies2")
	src := movieFolder(t, from)
	before := tree(t, src)
	dst := filepath.Join(to, "Heat (1995)")

	mv, err := StartMove(context.Background(), []MoveUnit{{Src: src, Dst: dst, Dir: true}}, MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !gone(src) || tree(t, dst) != before {
		t.Fatalf("after the move: source gone=%v, new place:\n%s", gone(src), tree(t, dst))
	}
	if left := mv.Commit(); len(left) != 0 {
		t.Fatalf("commit left %v", left)
	}

	// Moving it back through rollback.
	mv2, err := StartMove(context.Background(), []MoveUnit{{Src: dst, Dst: src, Dir: true}}, MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mv2.Rollback()
	if tree(t, dst) != before || !gone(src) {
		t.Fatalf("rollback did not put it back:\n%s", tree(t, dst))
	}
}

func TestMoveFolderByCopyKeepsTheOriginalUntilCommit(t *testing.T) {
	work := t.TempDir()
	from, to := filepath.Join(work, "movies"), filepath.Join(work, "elsewhere", "movies2")
	src := movieFolder(t, from)
	before := tree(t, src)
	old := time.Date(2020, 5, 17, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(src, "Heat (1995).mkv"), old, old); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(to, "Heat (1995)")

	var copied int64
	mv, err := StartMove(context.Background(), []MoveUnit{{Src: src, Dst: dst, Dir: true}}, MoveOptions{ForceCopy: true, Progress: func(n int64) { copied += n }})
	if err != nil {
		t.Fatal(err)
	}
	if tree(t, dst) != before {
		t.Fatalf("the copy differs:\n%s\nwant\n%s", tree(t, dst), before)
	}
	if tree(t, src) != before {
		t.Fatal("the original must stay until Commit")
	}
	// The progress counts the copy only, not the read-back check.
	if want := int64(5000 + len("subtitles") + len("<movie/>") + len("extra")); copied != want {
		t.Errorf("progress counted %d bytes, want %d", copied, want)
	}
	if info, _ := os.Stat(filepath.Join(dst, "Heat (1995).mkv")); !info.ModTime().Equal(old) {
		t.Errorf("modification time = %v, want %v", info.ModTime(), old)
	}
	if entries, _ := os.ReadDir(to); len(entries) != 1 {
		t.Errorf("leftovers beside the new folder: %v", entries)
	}

	// Someone drops a new file into the old folder meanwhile: it is not ours to delete.
	write(t, filepath.Join(src, "new-file.txt"), "kept")
	if left := mv.Commit(); len(left) != 0 {
		t.Fatalf("commit left %v", left)
	}
	if !strings.Contains(tree(t, src), "new-file.txt") || gone(filepath.Join(src, "new-file.txt")) {
		t.Fatalf("a file that was not copied was removed:\n%s", tree(t, src))
	}
	for _, f := range []string{"Heat (1995).mkv", "Heat (1995).en.srt", "movie.nfo", "extras/Making of.mkv"} {
		if !gone(filepath.Join(src, filepath.FromSlash(f))) {
			t.Errorf("%s was not removed from the original", f)
		}
	}
	if read(t, filepath.Join(dst, "extras", "Making of.mkv")) != "extra" {
		t.Error("the copy is wrong")
	}
}

func TestMoveFolderByCopyRemovesTheOriginalFolderWhenEmpty(t *testing.T) {
	work := t.TempDir()
	src := movieFolder(t, filepath.Join(work, "movies"))
	dst := filepath.Join(work, "other", "Heat (1995)")
	mv, err := StartMove(context.Background(), []MoveUnit{{Src: src, Dst: dst, Dir: true}}, MoveOptions{ForceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	mv.Commit()
	if !gone(src) {
		t.Fatalf("the original folder is still there:\n%s", tree(t, src))
	}
}

func TestMoveFilesAndSidecars(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "movies")
	write(t, filepath.Join(root, "Alien (1979).mkv"), "video")
	write(t, filepath.Join(root, "Alien (1979).en.srt"), "subs")
	write(t, filepath.Join(root, "Aliens (1986).mkv"), "another movie")
	to := filepath.Join(work, "movies2")
	for _, force := range []bool{false, true} {
		units := []MoveUnit{
			{Src: filepath.Join(root, "Alien (1979).mkv"), Dst: filepath.Join(to, "Alien (1979).mkv")},
			{Src: filepath.Join(root, "Alien (1979).en.srt"), Dst: filepath.Join(to, "Alien (1979).en.srt")},
		}
		mv, err := StartMove(context.Background(), units, MoveOptions{ForceCopy: force})
		if err != nil {
			t.Fatal(err)
		}
		mv.Commit()
		if read(t, filepath.Join(to, "Alien (1979).mkv")) != "video" || read(t, filepath.Join(to, "Alien (1979).en.srt")) != "subs" {
			t.Fatalf("force=%v: files are not in the new place", force)
		}
		if !gone(units[0].Src) || !gone(units[1].Src) {
			t.Fatalf("force=%v: the originals are still there", force)
		}
		if read(t, filepath.Join(root, "Aliens (1986).mkv")) != "another movie" {
			t.Fatal("another movie was touched")
		}
		// Put them back for the second round.
		for _, u := range units {
			write(t, u.Src, read(t, u.Dst))
			_ = os.Remove(u.Dst)
		}
	}
}

func TestMoveNeverOverwrites(t *testing.T) {
	work := t.TempDir()
	src := movieFolder(t, filepath.Join(work, "movies"))
	other := filepath.Join(work, "movies2")
	dst := filepath.Join(other, "Heat (1995)")
	write(t, filepath.Join(dst, "precious.txt"), "do not touch")
	for _, force := range []bool{false, true} {
		_, err := StartMove(context.Background(), []MoveUnit{{Src: src, Dst: dst, Dir: true}}, MoveOptions{ForceCopy: force})
		if !errors.Is(err, ErrMoveTargetExists) {
			t.Fatalf("force=%v: err = %v, want ErrMoveTargetExists", force, err)
		}
		if read(t, filepath.Join(dst, "precious.txt")) != "do not touch" || tree(t, dst) != "precious.txt" {
			t.Fatalf("force=%v: the folder in the way was changed:\n%s", force, tree(t, dst))
		}
		if !strings.Contains(tree(t, src), "Heat (1995).mkv") {
			t.Fatalf("force=%v: the original was touched", force)
		}
	}
	// And the same for a single file.
	f := filepath.Join(work, "a.mkv")
	write(t, f, "mine")
	g := filepath.Join(work, "b.mkv")
	write(t, g, "theirs")
	for _, force := range []bool{false, true} {
		if _, err := StartMove(context.Background(), []MoveUnit{{Src: f, Dst: g}}, MoveOptions{ForceCopy: force}); !errors.Is(err, ErrMoveTargetExists) {
			t.Fatalf("file, force=%v: err = %v", force, err)
		}
		if read(t, g) != "theirs" || read(t, f) != "mine" {
			t.Fatalf("file, force=%v: something changed", force)
		}
	}
}

func TestMoveUndoesTheUnitsAlreadyDoneWhenOneFails(t *testing.T) {
	for _, force := range []bool{false, true} {
		work := t.TempDir()
		a := filepath.Join(work, "movies", "A.mkv")
		b := filepath.Join(work, "movies", "B.mkv")
		write(t, a, "aaa")
		write(t, b, "bbb")
		to := filepath.Join(work, "new", "deeper")
		write(t, filepath.Join(to, "B.mkv"), "in the way")

		_, err := StartMove(context.Background(), []MoveUnit{
			{Src: a, Dst: filepath.Join(to, "A.mkv")},
			{Src: b, Dst: filepath.Join(to, "B.mkv")},
		}, MoveOptions{ForceCopy: force})
		if !errors.Is(err, ErrMoveTargetExists) {
			t.Fatalf("force=%v: err = %v", force, err)
		}
		if read(t, a) != "aaa" || read(t, b) != "bbb" {
			t.Fatalf("force=%v: the originals are not as they were", force)
		}
		if !gone(filepath.Join(to, "A.mkv")) {
			t.Fatalf("force=%v: the first file is still in the new place", force)
		}
		if read(t, filepath.Join(to, "B.mkv")) != "in the way" {
			t.Fatalf("force=%v: the file in the way changed", force)
		}
	}
}

func TestMoveRollbackRemovesFoldersItMade(t *testing.T) {
	work := t.TempDir()
	a := filepath.Join(work, "movies", "A.mkv")
	write(t, a, "aaa")
	dst := filepath.Join(work, "new", "deeper", "A.mkv")
	mv, err := StartMove(context.Background(), []MoveUnit{{Src: a, Dst: dst}}, MoveOptions{ForceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	if left := mv.Rollback(); len(left) != 0 {
		t.Fatalf("rollback left %v", left)
	}
	if !gone(filepath.Join(work, "new")) {
		t.Fatalf("folders made for the move are still there:\n%s", tree(t, work))
	}
	if read(t, a) != "aaa" {
		t.Fatal("the original is gone")
	}
	// A folder that was there before is kept.
	write(t, filepath.Join(work, "kept", "x"), "x")
	mv, err = StartMove(context.Background(), []MoveUnit{{Src: a, Dst: filepath.Join(work, "kept", "sub", "A.mkv")}}, MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mv.Rollback()
	if gone(filepath.Join(work, "kept")) || !gone(filepath.Join(work, "kept", "sub")) {
		t.Fatalf("rollback tidied the wrong folders:\n%s", tree(t, work))
	}
}

func TestMoveRefusesLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need rights on Windows")
	}
	work := t.TempDir()
	src := movieFolder(t, filepath.Join(work, "movies"))
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link.txt")); err != nil {
		t.Skip(err)
	}
	dst := filepath.Join(work, "other", "Heat (1995)")
	_, err := StartMove(context.Background(), []MoveUnit{{Src: src, Dst: dst, Dir: true}}, MoveOptions{ForceCopy: true})
	if err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("err = %v, want a refusal because of the link", err)
	}
	if !gone(dst) || !gone(filepath.Join(work, "other", ".Heat (1995)"+movingSuffix)) {
		t.Fatalf("a refused move left something behind:\n%s", tree(t, filepath.Join(work, "other")))
	}
	// A link as the thing to move is refused too.
	l := filepath.Join(work, "l.mkv")
	if err := os.Symlink(filepath.Join(src, "Heat (1995).mkv"), l); err != nil {
		t.Skip(err)
	}
	if _, err := StartMove(context.Background(), []MoveUnit{{Src: l, Dst: filepath.Join(work, "m.mkv")}}, MoveOptions{}); err == nil {
		t.Fatal("a link was moved")
	}
}

func TestMoveStopsWhenCancelledAndLeavesNothing(t *testing.T) {
	work := t.TempDir()
	src := filepath.Join(work, "movies", "Big")
	write(t, filepath.Join(src, "big.mkv"), strings.Repeat("x", 4<<20))
	write(t, filepath.Join(src, "small.nfo"), "n")
	dst := filepath.Join(work, "other", "Big")

	ctx, cancel := context.WithCancel(context.Background())
	var seen int64
	_, err := StartMove(ctx, []MoveUnit{{Src: src, Dst: dst, Dir: true}}, MoveOptions{ForceCopy: true, Progress: func(n int64) {
		seen += n
		if seen > 1<<20 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !gone(dst) {
		t.Fatal("a half copy was left in the new place")
	}
	if entries, _ := os.ReadDir(filepath.Join(work, "other")); len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
	if read(t, filepath.Join(src, "small.nfo")) != "n" || len(read(t, filepath.Join(src, "big.mkv"))) != 4<<20 {
		t.Fatal("the original was changed")
	}
}

func TestMoveClearsItsOwnStagingFolderFromAnEarlierTry(t *testing.T) {
	work := t.TempDir()
	src := movieFolder(t, filepath.Join(work, "movies"))
	to := filepath.Join(work, "other")
	staging := filepath.Join(to, ".Heat (1995)"+movingSuffix)
	write(t, filepath.Join(staging, movingMarker), "")
	write(t, filepath.Join(staging, "half.mkv"), "half a file")
	dst := filepath.Join(to, "Heat (1995)")
	mv, err := StartMove(context.Background(), []MoveUnit{{Src: src, Dst: dst, Dir: true}}, MoveOptions{ForceCopy: true})
	if err != nil {
		t.Fatalf("an earlier attempt's leftovers blocked the move: %v", err)
	}
	mv.Commit()
	if !gone(staging) || gone(filepath.Join(dst, "Heat (1995).mkv")) {
		t.Fatalf("after the move:\n%s", tree(t, to))
	}

	// A folder of that name that Mediarium did not make is never removed.
	src2 := movieFolder(t, filepath.Join(work, "movies3"))
	other := filepath.Join(work, "other2")
	foreign := filepath.Join(other, ".Heat (1995)"+movingSuffix)
	write(t, filepath.Join(foreign, "mine.txt"), "mine")
	if _, err := StartMove(context.Background(), []MoveUnit{{Src: src2, Dst: filepath.Join(other, "Heat (1995)"), Dir: true}}, MoveOptions{ForceCopy: true}); err == nil {
		t.Fatal("the move went ahead over a folder that is not ours")
	}
	if read(t, filepath.Join(foreign, "mine.txt")) != "mine" {
		t.Fatal("a folder that is not ours was changed")
	}
}

func TestVerifyFileCatchesADifferentCopy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	write(t, p, "hello")
	ctx := context.Background()
	good := sumOf("hello")
	if err := verifyFile(ctx, p, 5, good); err != nil {
		t.Fatal(err)
	}
	if err := verifyFile(ctx, p, 5, sumOf("hellp")); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a wrong checksum passed: %v", err)
	}
	if err := verifyFile(ctx, p, 6, good); err == nil {
		t.Fatal("a wrong size passed")
	}
	if err := verifyFile(ctx, p+".missing", 5, good); err == nil {
		t.Fatal("a missing file passed")
	}
}

func sumOf(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func TestMoveAppliesTheConfiguredFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	t.Cleanup(func() { fileperms.Set(fileperms.Modes{}) })
	work := t.TempDir()
	a := filepath.Join(work, "a.mkv")
	write(t, a, "aaa")
	if err := os.Chmod(a, 0o600); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(work, "b.mkv")
	// With no mode set the copy keeps the original's.
	if _, err := StartMove(context.Background(), []MoveUnit{{Src: a, Dst: b}}, MoveOptions{ForceCopy: true}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(b); info.Mode().Perm() != 0o600 {
		t.Errorf("copy has %o, want the original's 600", info.Mode().Perm())
	}
	_ = os.Remove(b)
	fileperms.Set(fileperms.Modes{File: 0o664})
	if _, err := StartMove(context.Background(), []MoveUnit{{Src: a, Dst: b}}, MoveOptions{ForceCopy: true}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(b); info.Mode().Perm() != 0o664 {
		t.Errorf("copy has %o, want the configured 664", info.Mode().Perm())
	}
}

func TestMapPath(t *testing.T) {
	units := []MoveUnit{
		{Src: "/movies/Heat (1995)", Dst: "/movies2/Heat (1995)", Dir: true},
		{Src: "/movies/Alien.mkv", Dst: "/movies2/Alien.mkv"},
	}
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/movies/Heat (1995)/Heat (1995).mkv", "/movies2/Heat (1995)/Heat (1995).mkv", true},
		{"/movies/Heat (1995)", "/movies2/Heat (1995)", true},
		{"/movies/Heat (1995)/extras/x.mkv", "/movies2/Heat (1995)/extras/x.mkv", true},
		{"/movies/Alien.mkv", "/movies2/Alien.mkv", true},
		{"/movies/Heat (1995) 2/x.mkv", "", false}, // a different folder with a similar name
		{"/movies/Other.mkv", "", false},
	}
	for _, c := range cases {
		got, ok := MapPath(units, filepath.FromSlash(c.in))
		if ok != c.ok || (ok && got != filepath.FromSlash(c.want)) {
			t.Errorf("MapPath(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
