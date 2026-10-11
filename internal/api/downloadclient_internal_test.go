package api

import (
	"path/filepath"
	"testing"
)

func TestMapClientPath(t *testing.T) {
	cases := []struct {
		name, path, remote, local, want string
	}{
		{"no mapping", "/downloads/Movie.mkv", "", "", "/downloads/Movie.mkv"},
		{"inside", "/downloads/Show.S01/ep1.mkv", "/downloads", "/data/torrents", "/data/torrents/Show.S01/ep1.mkv"},
		{"trailing slash", "/downloads/Movie", "/downloads/", "/data/torrents/", "/data/torrents/Movie"},
		{"the folder itself", "/downloads", "/downloads", "/data/torrents", "/data/torrents"},
		{"only a prefix of a name", "/downloads2/Movie", "/downloads", "/data/torrents", "/downloads2/Movie"},
		{"outside", "/other/Movie", "/downloads", "/data/torrents", "/other/Movie"},
		{"root", "/Movie", "/", "/data", "/data/Movie"},
		{"windows client", `D:\Torrents\Movie\a.mkv`, `D:\Torrents`, "/data/torrents", "/data/torrents/Movie/a.mkv"},
		{"windows any case", `d:\torrents\Movie`, `D:\Torrents`, "/data/torrents", "/data/torrents/Movie"},
		{"linux is case sensitive", "/Downloads/Movie", "/downloads", "/data/torrents", "/Downloads/Movie"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapClientPath(tc.path, tc.remote, tc.local)
			want := filepath.FromSlash(tc.want)
			if got != want {
				t.Errorf("mapClientPath(%q, %q, %q) = %q, want %q", tc.path, tc.remote, tc.local, got, want)
			}
		})
	}
}
