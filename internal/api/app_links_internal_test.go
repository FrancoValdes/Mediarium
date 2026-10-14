package api

import "testing"

func TestAppOrigin(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"http://192.168.1.20:8080/guide", "http://192.168.1.20:8080", true},
		{"https://Guide.Example.com/path?q=1", "https://guide.example.com", true},
		{"http://[fd00::20]:8080/", "http://[fd00::20]:8080", true},
		{"http://box", "http://box", true},
		{"http://x;sandbox", "", false},
		{"http://x;sandbox/", "", false},
		{"http://x/;sandbox", "", false},
		{"http://x'self'/", "", false},
		{"http://x y/", "", false},
		{"http://x\tdefault-src/", "", false},
		{"http://ex_ample.com/", "", false},
		{"http://x:/", "", false},
		{"http://x:0/", "", false},
		{"http://x:65536/", "", false},
		{"http://x:080/", "", false},
		{"http://[fe80::1%25eth0]/", "", false},
		{"http://user:pw@box/", "", false},
		{"ftp://box/", "", false},
		{"javascript:alert(1)", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := appOrigin(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("appOrigin(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
