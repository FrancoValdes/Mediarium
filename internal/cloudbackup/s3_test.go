package cloudbackup

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The worked examples in Amazon's S3 Signature Version 4 documentation
// ("Authenticating Requests: Using the Authorization Header").
func TestSignMatchesAmazonsExamples(t *testing.T) {
	s := &S3{Region: "us-east-1", AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	at := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		method  string
		url     string
		headers map[string]string
		payload string
		want    string
	}{
		{
			name:    "GET object",
			method:  http.MethodGet,
			url:     "https://examplebucket.s3.amazonaws.com/test.txt",
			headers: map[string]string{"Range": "bytes=0-9"},
			payload: emptyHash,
			want:    "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
		},
		{
			name:    "GET bucket list",
			method:  http.MethodGet,
			url:     "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J",
			payload: emptyHash,
			want:    "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7",
		},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, tt.url, nil)
		for k, v := range tt.headers {
			req.Header.Set(k, v)
		}
		s.sign(req, tt.payload, at)
		auth := req.Header.Get("Authorization")
		if !strings.HasSuffix(auth, "Signature="+tt.want) {
			t.Errorf("%s: %s\nwant signature %s", tt.name, auth, tt.want)
		}
	}
}

func TestObjectURLAndEscaping(t *testing.T) {
	s := &S3{Endpoint: "https://s3.eu-central-003.backblazeb2.com/", Bucket: "my-backups", Prefix: "/mediarium", PathStyle: true}
	u, err := s.objectURL(s.key("mediarium-backup-2026-10-06.zip.enc"))
	if err != nil || u.String() != "https://s3.eu-central-003.backblazeb2.com/my-backups/mediarium/mediarium-backup-2026-10-06.zip.enc" {
		t.Fatalf("path style: %v %v", u, err)
	}
	s.PathStyle = false
	if u, _ := s.objectURL("a b"); u.Host != "my-backups.s3.eu-central-003.backblazeb2.com" || u.Path != "/a b" {
		t.Fatalf("virtual host: %v", u)
	}
	if got := s3Escape("/test$file.text", true); got != "/test%24file.text" {
		t.Fatalf("escape: %s", got)
	}
	if _, err := (&S3{Endpoint: "not an address"}).objectURL("x"); err == nil {
		t.Fatal("a bad endpoint was accepted")
	}
}
