package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

var ErrFileTooLarge = errors.New("uploaded file exceeds the configured size limit")

type SavedFile struct {
	Path         string
	RelativePath string
	SizeBytes    int64
	SHA256       string
}

type Local struct {
	root     string
	maxBytes int64
}

func NewLocal(root string, maxBytes int64) (*Local, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return nil, errors.New("storage root is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("storage maxBytes must be positive")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &Local{root: root, maxBytes: maxBytes}, nil
}

func (s *Local) Root() string {
	return s.root
}

func (s *Local) Save(ctx context.Context, originalName string, source io.Reader) (SavedFile, error) {
	if err := ctx.Err(); err != nil {
		return SavedFile{}, err
	}
	if source == nil {
		return SavedFile{}, errors.New("file source is required")
	}

	extension := safeExtension(originalName)
	relativePath := filepath.Join(uuid.NewString()[:2], uuid.NewString()+extension)
	targetPath := filepath.Join(s.root, relativePath)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return SavedFile{}, fmt.Errorf("create upload directory: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(targetPath), ".upload-*")
	if err != nil {
		return SavedFile{}, fmt.Errorf("create temporary upload: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	hasher := sha256.New()
	limited := &io.LimitedReader{R: source, N: s.maxBytes + 1}
	written, err := copyWithContext(ctx, io.MultiWriter(temporary, hasher), limited)
	if err != nil {
		return SavedFile{}, fmt.Errorf("store upload: %w", err)
	}
	if written > s.maxBytes {
		return SavedFile{}, ErrFileTooLarge
	}
	if err := temporary.Sync(); err != nil {
		return SavedFile{}, fmt.Errorf("sync upload: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return SavedFile{}, fmt.Errorf("close upload: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o640); err != nil {
		return SavedFile{}, fmt.Errorf("set upload permissions: %w", err)
	}
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		return SavedFile{}, fmt.Errorf("commit upload: %w", err)
	}
	committed = true

	return SavedFile{
		Path:         targetPath,
		RelativePath: filepath.ToSlash(relativePath),
		SizeBytes:    written,
		SHA256:       hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func (s *Local) Open(relativePath string) (*os.File, error) {
	path, err := s.Resolve(relativePath)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *Local) Resolve(relativePath string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(relativePath))
	if cleaned == "" || cleaned == "." || filepath.IsAbs(cleaned) {
		return "", errors.New("invalid storage path")
	}
	resolved := filepath.Join(s.root, cleaned)
	relative, err := filepath.Rel(s.root, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve storage path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("storage path escapes root")
	}
	return resolved, nil
}

func safeExtension(name string) string {
	extension := strings.ToLower(filepath.Ext(filepath.Base(name)))
	if len(extension) > 12 {
		return ""
	}
	for _, character := range extension {
		if character == '.' || character == '-' || character == '_' ||
			(character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') {
			continue
		}
		return ""
	}
	return extension
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}
