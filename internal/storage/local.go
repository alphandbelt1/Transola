package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Local is a filesystem-backed storage backend.
type Local struct {
	root string
}

// NewLocal configures a Local backend rooted at path.
func NewLocal(root string) (*Local, error) {
	if root == "" {
		return nil, errors.New("data root is required")
	}
	return &Local{root: root}, nil
}

// Ready ensures the storage root exists and is writable.
func (l *Local) Ready(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return fmt.Errorf("create data root: %w", err)
	}

	testFile := filepath.Join(l.root, ".transola_write_test")
	if err := ioutil.WriteFile(testFile, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("write test file: %w", err)
	}
	_ = os.Remove(testFile)

	return nil
}

// Root returns the configured root path.
func (l *Local) Root() string {
	return l.root
}

// Save streams data to a new object, verifies checksum if provided, and returns the reference.
func (l *Local) Save(ctx context.Context, name string, r io.Reader, expectedSHA string) (ObjectRef, error) {
	safeName := sanitizeName(name)
	now := time.Now().UTC()
	key := fmt.Sprintf("%04d/%02d/%02d/%s_%s", now.Year(), now.Month(), now.Day(), randString(8), safeName)
	fullPath := filepath.Join(l.root, key)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return ObjectRef{}, fmt.Errorf("mkdir: %w", err)
	}

	tmpPath := fullPath + ".uploading"
	f, err := os.Create(tmpPath)
	if err != nil {
		return ObjectRef{}, fmt.Errorf("create temp: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	size, err := copyWithContext(ctx, io.MultiWriter(f, hasher), r)
	if err != nil {
		_ = os.Remove(tmpPath)
		return ObjectRef{}, fmt.Errorf("write: %w", err)
	}
	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA != "" && !strings.EqualFold(expectedSHA, actualSHA) {
		_ = os.Remove(tmpPath)
		return ObjectRef{}, fmt.Errorf("checksum mismatch: expected %s got %s", expectedSHA, actualSHA)
	}

	if err := os.Rename(tmpPath, fullPath); err != nil {
		return ObjectRef{}, fmt.Errorf("commit file: %w", err)
	}

	return ObjectRef{Key: key, Size: size, SHA256: actualSHA}, nil
}

// Open returns a reader for an object key.
func (l *Local) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	path := filepath.Join(l.root, key)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return os.Open(path)
}

func sanitizeName(name string) string {
	base := filepath.Base(name)
	base = strings.TrimSpace(base)
	if base == "." || base == "" {
		return "file"
	}
	return base
}

func randString(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func copyWithContext(ctx context.Context, w io.Writer, r io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		n, readErr := r.Read(buf)
		if n > 0 {
			written, wErr := w.Write(buf[:n])
			total += int64(written)
			if wErr != nil {
				return total, wErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}
