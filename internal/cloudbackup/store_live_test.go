package cloudbackup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"
)

// Upload, list and delete against real servers. Skipped unless pointed at
// them, for example (S3 via MinIO, WebDAV via rclone):
//
//	MEDIARIUM_TEST_S3=http://minio:9000 MEDIARIUM_TEST_S3_KEY=... MEDIARIUM_TEST_S3_SECRET=... \
//	MEDIARIUM_TEST_S3_BUCKET=backups MEDIARIUM_TEST_WEBDAV=http://webdav:8080/backups/ \
//	go test ./internal/cloudbackup -run Live
func TestLiveStores(t *testing.T) {
	var stores []struct {
		name  string
		store Store
	}
	if ep := os.Getenv("MEDIARIUM_TEST_S3"); ep != "" {
		stores = append(stores, struct {
			name  string
			store Store
		}{"s3", &S3{Endpoint: ep, Region: "us-east-1", Bucket: os.Getenv("MEDIARIUM_TEST_S3_BUCKET"), Prefix: "mediarium/", AccessKey: os.Getenv("MEDIARIUM_TEST_S3_KEY"), SecretKey: os.Getenv("MEDIARIUM_TEST_S3_SECRET"), PathStyle: true}})
	}
	if u := os.Getenv("MEDIARIUM_TEST_WEBDAV"); u != "" {
		stores = append(stores, struct {
			name  string
			store Store
		}{"webdav", &WebDAV{URL: u, Username: os.Getenv("MEDIARIUM_TEST_WEBDAV_USER"), Password: os.Getenv("MEDIARIUM_TEST_WEBDAV_PASS")}})
	}
	if len(stores) == 0 {
		t.Skip("no live storage configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, st := range stores {
		t.Run(st.name, func(t *testing.T) {
			data := make([]byte, 300_000)
			rand.Read(data)
			sum := sha256.Sum256(data)
			names := []string{"mediarium-backup-2026-10-06-0300.zip.enc", "mediarium-backup-2026-10-07-0300.zip.enc"}
			for _, n := range names {
				if err := st.store.Put(ctx, n, bytes.NewReader(data), int64(len(data)), hex.EncodeToString(sum[:])); err != nil {
					t.Fatalf("put %s: %v", n, err)
				}
			}
			list, err := st.store.List(ctx)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			found := 0
			for _, o := range list {
				for _, n := range names {
					if o.Name == n && o.Size == int64(len(data)) {
						found++
					}
				}
			}
			if found != 2 {
				t.Fatalf("list: %+v", list)
			}
			for _, n := range names {
				if err := st.store.Delete(ctx, n); err != nil {
					t.Fatalf("delete %s: %v", n, err)
				}
			}
			if list, err := st.store.List(ctx); err != nil || len(list) != 0 {
				t.Fatalf("after delete: %+v %v", list, err)
			}
		})
	}
}
