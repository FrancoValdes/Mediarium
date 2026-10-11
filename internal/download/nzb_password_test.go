package download

import (
	"strings"
	"testing"
)

const nzbWithHead = `<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
<head><meta type="title">Some.Release</meta><meta type="password"> s3cret </meta></head>
<file subject="a.rar" poster="x"><groups><group>alt.binaries.test</group></groups>
<segments><segment bytes="100" number="1">abc@example</segment></segments></file>
</nzb>`

func TestNZBHeadPassword(t *testing.T) {
	n, err := ParseNZB([]byte(nzbWithHead))
	if err != nil {
		t.Fatal(err)
	}
	if n.Password != "s3cret" {
		t.Fatalf("password = %q, want s3cret", n.Password)
	}
	plain, err := ParseNZB([]byte(strings.Replace(nzbWithHead, `<meta type="password"> s3cret </meta>`, "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Password != "" {
		t.Fatalf("password = %q, want none", plain.Password)
	}
}

func TestPasswordFromTitle(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"My.Movie.2024.1080p{{secret}}", "secret"},
		{"My.Movie.2024{{pa ss}}", "pa ss"},
		{"My.Movie.2024", ""},
		{"My.Movie.2024{{unclosed", ""},
		{"My.Movie.2024{{}}", ""},
		{"My.Movie.2024{{" + strings.Repeat("x", 300) + "}}", ""},
		{"My.Movie.2024{{bad\x01pw}}", ""},
	} {
		if got := PasswordFromTitle(tc.title); got != tc.want {
			t.Errorf("PasswordFromTitle(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}
