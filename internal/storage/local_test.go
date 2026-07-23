package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestLocalSaveAndResolve(t *testing.T) {
	store, err := NewLocal(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := store.Save(context.Background(), "../../creative.PNG", strings.NewReader("image-data"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.SizeBytes != int64(len("image-data")) {
		t.Fatalf("unexpected size: %d", saved.SizeBytes)
	}
	if !strings.HasSuffix(saved.RelativePath, ".png") {
		t.Fatalf("extension not preserved safely: %s", saved.RelativePath)
	}
	data, err := os.ReadFile(saved.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-data" {
		t.Fatalf("unexpected contents: %q", data)
	}
}

func TestLocalRejectsOversizedFile(t *testing.T) {
	store, err := NewLocal(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Save(context.Background(), "video.mp4", strings.NewReader("12345"))
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}
}

func TestLocalRejectsEscapingPath(t *testing.T) {
	store, err := NewLocal(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve("../secret"); err == nil {
		t.Fatal("expected path traversal rejection")
	}
	if _, err := store.Save(context.Background(), "empty", io.LimitReader(strings.NewReader("ok"), 2)); err != nil {
		t.Fatal(err)
	}
}
