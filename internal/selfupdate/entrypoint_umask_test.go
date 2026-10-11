package selfupdate

import (
	"os"
	"strings"
	"testing"
)

// UMASK is set before the app starts, the way the linuxserver.io images do it.
// The "app" here is a small script that answers --version-check like Mediarium
// and otherwise prints the umask it was started with.
func TestEntrypointAppliesUMASK(t *testing.T) {
	e := newEntry(t, "1.0.0")

	// The platform word has to match what the real program says, since the
	// entrypoint compares the two; take it from the real one.
	realOut, errText, code := e.run(nil, "IMAGE", "--version-check")
	if code != 0 {
		t.Fatalf("the real program did not answer --version-check: %q (%s)", realOut, errText)
	}
	fields := strings.Fields(realOut)
	if len(fields) != 3 {
		t.Fatalf("unexpected --version-check output %q", realOut)
	}
	fake := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --version-check) echo \"mediarium 1.0.0 " + fields[2] + "\" ;;\n" +
		"  --accepts-update) exit 0 ;;\n" +
		"  *) umask ;;\n" +
		"esac\n"
	_ = os.Remove(e.image) // a new file: the old program may still be shutting down
	if err := os.WriteFile(e.image, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(e.image, 0o755); err != nil {
		t.Fatal(err)
	}

	umaskWith := func(env ...string) (string, string) {
		t.Helper()
		out, errText, code := e.run(env, "IMAGE", "run")
		if code != 0 {
			t.Fatalf("entrypoint exited %d: %s", code, errText)
		}
		return strings.TrimSpace(out), errText
	}

	baseline, errText := umaskWith()
	if strings.Contains(errText, "UMASK") {
		t.Errorf("no UMASK is set, yet it says: %s", errText)
	}

	cases := []struct {
		name     string
		env      string
		wantMask string // as `umask` prints it, four digits; "" = unchanged
		wantNote string
	}{
		{name: "002", env: "UMASK=002", wantMask: "0002"},
		{name: "022", env: "UMASK=022", wantMask: "0022"},
		{name: "four digits", env: "UMASK=0077", wantMask: "0077"},
		{name: "not octal is ignored with a note", env: "UMASK=089", wantNote: "ignoring UMASK=089"},
		{name: "letters are ignored with a note", env: "UMASK=u=rwx", wantNote: "ignoring UMASK=u=rwx"},
		{name: "too long is ignored with a note", env: "UMASK=00022", wantNote: "ignoring UMASK=00022"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, errText := umaskWith(c.env)
			want := c.wantMask
			if want == "" {
				want = baseline
			}
			if got != want {
				t.Errorf("umask = %q, want %q (stderr: %s)", got, want, errText)
			}
			if c.wantNote != "" && !strings.Contains(errText, c.wantNote) {
				t.Errorf("stderr %q does not say %q", errText, c.wantNote)
			}
			if c.wantNote == "" && strings.Contains(errText, "UMASK") {
				t.Errorf("unexpected note about UMASK: %s", errText)
			}
		})
	}
}
