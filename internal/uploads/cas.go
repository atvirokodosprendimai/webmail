// Package uploads implements a content-addressed filesystem blobstore.
// Path = <root>/<sha256[:2]>/<sha256>. Same sha256 → one file on disk
// regardless of how many projects reference it.
package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

type Store struct {
	Root string
}

func (s *Store) ensureDir(prefix string) (string, error) {
	dir := filepath.Join(s.Root, prefix)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Write stores bytes under their sha256. Returns (sha256_hex, size).
// Atomic via tmp + rename. No-op (returns existing path) when the same
// content is already stored.
func (s *Store) Write(buf []byte) (string, int64, error) {
	sum := sha256.Sum256(buf)
	hex := hex.EncodeToString(sum[:])
	dir, err := s.ensureDir(hex[:2])
	if err != nil {
		return "", 0, err
	}
	final := filepath.Join(dir, hex)
	if _, err := os.Stat(final); err == nil {
		return hex, int64(len(buf)), nil
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", 0, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", 0, err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return "", 0, err
	}
	return hex, int64(len(buf)), nil
}

// Open returns a reader for the stored content at sha.
func (s *Store) Open(sha string) (*os.File, error) {
	if len(sha) < 4 {
		return nil, errors.New("uploads: bad sha")
	}
	return os.Open(filepath.Join(s.Root, sha[:2], sha))
}

// Serve streams the blob to the HTTP response with Content-Type +
// Content-Disposition headers.
func (s *Store) Serve(w http.ResponseWriter, r *http.Request, sha, filename, mime string) {
	f, err := s.Open(sha)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "stat", http.StatusInternalServerError)
		return
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	http.ServeContent(w, r, filename, stat.ModTime(), f)
}

// EnsureRoot creates the root upload dir if missing.
func (s *Store) EnsureRoot() error {
	return os.MkdirAll(s.Root, 0o755)
}

var _ io.Reader = (*os.File)(nil)
