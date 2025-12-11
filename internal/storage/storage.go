package storage

import (
	"context"
	"io"
)

// Backend represents a storage driver.
type Backend interface {
	Ready(ctx context.Context) error
	Root() string
	Save(ctx context.Context, name string, r io.Reader, expectedSHA string) (ObjectRef, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// ObjectRef is the persisted file pointer.
type ObjectRef struct {
	Key    string
	Size   int64
	SHA256 string
}
