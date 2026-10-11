package ereader_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rdborg/mediarium/internal/ereader"
	"github.com/rdborg/mediarium/internal/store"
)

type fakeBox struct{}

func (fakeBox) Encrypt(s string) (string, error) { return "enc:" + s, nil }
func (fakeBox) Decrypt(s string) (string, error) { return strings.TrimPrefix(s, "enc:"), nil }

func newRepo(t *testing.T) (*ereader.Repo, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return ereader.NewRepo(db, fakeBox{}), db
}

func addUser(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO users (username, password_hash) VALUES (?, 'x')`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestOPDSPassword(t *testing.T) {
	repo, db := newRepo(t)
	uid := addUser(t, db, "ryan")
	if acc, _ := repo.Get(uid); acc.OPDS || acc.Kobo {
		t.Fatalf("nothing is on by default: %+v", acc)
	}
	if repo.CheckOPDS(uid, "anything") || repo.CheckOPDS(uid, "") {
		t.Fatal("an account without a password must accept none")
	}
	pw, err := repo.NewOPDSPassword(uid)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^([a-z2-9]{4}-){5}[a-z2-9]{4}$`).MatchString(pw) {
		t.Fatalf("password shape: %q", pw)
	}
	for _, ok := range []string{pw, strings.ToUpper(pw), strings.ReplaceAll(pw, "-", ""), " " + pw + " ", strings.ReplaceAll(pw, "-", " ")} {
		if !repo.CheckOPDS(uid, ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", "wrong", pw + "x", pw[:len(pw)-1]} {
		if repo.CheckOPDS(uid, bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
	// Only a hash of it is kept.
	var stored string
	if err := db.QueryRow(`SELECT opds_hash FROM ereader_access WHERE user_id = ?`, uid).Scan(&stored); err != nil || strings.Contains(stored, pw[:4]) || len(stored) != 64 {
		t.Errorf("stored form %q (%v)", stored, err)
	}
	// Another account's password is not this one's.
	other := addUser(t, db, "sam")
	if repo.CheckOPDS(other, pw) {
		t.Error("a password works for another account")
	}
	// A new one replaces the old.
	pw2, _ := repo.NewOPDSPassword(uid)
	if pw2 == pw || repo.CheckOPDS(uid, pw) || !repo.CheckOPDS(uid, pw2) {
		t.Error("a new password should replace the old one")
	}
	if err := repo.RemoveOPDS(uid); err != nil || repo.CheckOPDS(uid, pw2) {
		t.Errorf("removing the password: %v", err)
	}
}

func TestKoboToken(t *testing.T) {
	repo, db := newRepo(t)
	uid := addUser(t, db, "ryan")
	other := addUser(t, db, "sam")
	tok, err := repo.NewKoboToken(uid)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(tok) {
		t.Fatalf("token shape: %q", tok)
	}
	if got, err := repo.UserForKobo(tok); err != nil || got != uid {
		t.Fatalf("lookup: %d %v", got, err)
	}
	if again, _ := repo.KoboToken(uid); again != tok {
		t.Errorf("the address can be seen again: %q", again)
	}
	for _, bad := range []string{"", "short", strings.Repeat("0", 64), tok[:63], tok + "0", strings.ToUpper(tok)} {
		if _, err := repo.UserForKobo(bad); !errors.Is(err, ereader.ErrNotFound) {
			t.Errorf("%q should be unknown, got %v", bad, err)
		}
	}
	// The token itself is not in the database in the clear.
	var hash, enc string
	_ = db.QueryRow(`SELECT kobo_hash, kobo_token_enc FROM ereader_access WHERE user_id = ?`, uid).Scan(&hash, &enc)
	if hash == tok || strings.Contains(hash, tok) {
		t.Error("the lookup column holds the token itself")
	}
	if tok2, _ := repo.NewKoboToken(other); tok2 == tok {
		t.Error("two accounts got the same token")
	}
	// A new token replaces the old one.
	tok3, _ := repo.NewKoboToken(uid)
	if _, err := repo.UserForKobo(tok); !errors.Is(err, ereader.ErrNotFound) {
		t.Error("the old address still works")
	}
	if got, _ := repo.UserForKobo(tok3); got != uid {
		t.Error("the new address doesn't work")
	}
	if err := repo.RemoveKobo(uid); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UserForKobo(tok3); !errors.Is(err, ereader.ErrNotFound) {
		t.Error("the address works after it was turned off")
	}
	if tok, _ := repo.KoboToken(uid); tok != "" {
		t.Error("the token is still shown")
	}
}

func TestKoboLibraryAndState(t *testing.T) {
	repo, db := newRepo(t)
	uid := addUser(t, db, "ryan")
	if _, err := db.Exec(`INSERT INTO books (ol_key, title) VALUES ('OL1W', 'T')`); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkSynced(uid, 1, "T", "10:1"); err != nil {
		t.Fatal(err)
	}
	lib, _ := repo.Library(uid)
	if e := lib[1]; e.Title != "T" || e.FileSig != "10:1" || e.Archived {
		t.Fatalf("library: %+v", lib)
	}
	if err := repo.Archive(uid, 1); err != nil {
		t.Fatal(err)
	}
	if lib, _ = repo.Library(uid); !lib[1].Archived {
		t.Error("archive didn't stick")
	}
	// Archiving a book never sent still remembers it.
	if err := repo.Archive(uid, 99); err != nil {
		t.Fatal(err)
	}
	if lib, _ = repo.Library(uid); !lib[99].Archived {
		t.Error("archive of an unsent book")
	}

	pct := 12.5
	if err := repo.SaveState(uid, ereader.State{BookID: 1, Status: "Reading", LocationValue: "kobo.1.1", ContentPercent: &pct, TimesStarted: 2, DeviceTS: "2026-10-09T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	st, ok, err := repo.GetState(uid, 1)
	if err != nil || !ok || st.Status != "Reading" || st.ContentPercent == nil || *st.ContentPercent != 12.5 || st.TimesStarted != 2 {
		t.Fatalf("state: %+v %v %v", st, ok, err)
	}
	if _, ok, _ := repo.GetState(uid, 2); ok {
		t.Error("a state from nowhere")
	}
	if err := repo.ResetKobo(uid); err != nil {
		t.Fatal(err)
	}
	if lib, _ = repo.Library(uid); len(lib) != 0 {
		t.Errorf("reset left %v", lib)
	}
	if st, _, _ = repo.GetState(uid, 1); st.DeviceTS != "" || st.Status != "Reading" {
		t.Errorf("reset should forget only what the device has: %+v", st)
	}
	// Removing the book removes what the Kobo said about it.
	if _, err := db.Exec(`DELETE FROM books WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := repo.GetState(uid, 1); ok {
		t.Error("the state of a removed book stays")
	}
}
