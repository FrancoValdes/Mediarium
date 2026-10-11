package api

import (
	"context"
	"io"
	"slices"
	"testing"

	"github.com/rdborg/mediarium/internal/cloudbackup"
)

type fakeStore struct{ names []string }

func (f *fakeStore) Put(context.Context, string, io.Reader, int64, string) error { return nil }

func (f *fakeStore) List(context.Context) ([]cloudbackup.Object, error) {
	var out []cloudbackup.Object
	for _, n := range f.names {
		out = append(out, cloudbackup.Object{Name: n})
	}
	return out, nil
}

func (f *fakeStore) Delete(_ context.Context, name string) error {
	f.names = slices.DeleteFunc(f.names, func(n string) bool { return n == name })
	return nil
}

func TestPruneCloudKeepsTheNewestBackupsOnly(t *testing.T) {
	tests := []struct {
		name  string
		keep  int
		files []string
		want  []string
	}{
		{"under the limit", 3,
			[]string{"mediarium-backup-20261001-030000.zip.enc", "mediarium-backup-20261002-030000.zip.enc"},
			[]string{"mediarium-backup-20261001-030000.zip.enc", "mediarium-backup-20261002-030000.zip.enc"}},
		{"oldest go, whatever the list order", 2,
			[]string{"mediarium-backup-20261003-030000.zip.enc", "mediarium-backup-20260930-030000.zip.enc", "mediarium-backup-20261005-030000.zip.enc", "mediarium-backup-20261001-030000.zip.enc"},
			[]string{"mediarium-backup-20261003-030000.zip.enc", "mediarium-backup-20261005-030000.zip.enc"}},
		{"other files are never touched", 1,
			[]string{"notes.txt", "mediarium-backup-20261001-030000.zip", "mediarium-backup-20261001-030000.zip.enc", "mediarium-backup-20261002-030000.zip.enc"},
			[]string{"notes.txt", "mediarium-backup-20261001-030000.zip", "mediarium-backup-20261002-030000.zip.enc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeStore{names: slices.Clone(tt.files)}
			if err := pruneCloud(context.Background(), f, tt.keep); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.names, tt.want) {
				t.Fatalf("left %v, want %v", f.names, tt.want)
			}
		})
	}
}

func TestCloudConfigCheck(t *testing.T) {
	good := cloudConfig{Kind: "s3", Endpoint: "https://s3.example.com", Bucket: "b", AccessKey: "a", SecretKey: "s", Passphrase: "long enough pass"}
	tests := []struct {
		name   string
		change func(*cloudConfig)
		ok     bool
	}{
		{"complete", func(*cloudConfig) {}, true},
		{"no kind", func(c *cloudConfig) { c.Kind = "" }, false},
		{"endpoint without scheme", func(c *cloudConfig) { c.Endpoint = "s3.example.com" }, false},
		{"no bucket", func(c *cloudConfig) { c.Bucket = "" }, false},
		{"no secret", func(c *cloudConfig) { c.SecretKey = "" }, false},
		{"short passphrase", func(c *cloudConfig) { c.Passphrase = "short" }, false},
		{"webdav", func(c *cloudConfig) {
			*c = cloudConfig{Kind: "webdav", URL: "https://nas.local/dav", Passphrase: "long enough pass"}
		}, true},
		{"webdav without address", func(c *cloudConfig) { *c = cloudConfig{Kind: "webdav", Passphrase: "long enough pass"} }, false},
	}
	for _, tt := range tests {
		c := good
		tt.change(&c)
		if err := c.check(); (err == nil) != tt.ok {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}
