package cloudbackup

import (
	"context"
	"io"
	"time"
)

// Object is one backup in the storage.
type Object struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// Store is somewhere backups can be copied to.
type Store interface {
	// Put uploads size bytes from body as name; hash is the body's hex SHA-256
	// (S3 signs it; WebDAV ignores it).
	Put(ctx context.Context, name string, body io.Reader, size int64, hash string) error
	List(ctx context.Context) ([]Object, error)
	Delete(ctx context.Context, name string) error
}
