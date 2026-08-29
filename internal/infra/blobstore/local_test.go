package blobstore

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestLocalContentAddressedPutAndOpen(t *testing.T) {
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Put(context.Background(), strings.NewReader("same content"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(context.Background(), bytes.NewBufferString("same content"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.SizeBytes != 12 {
		t.Fatalf("content addressed put not idempotent: first=%+v second=%+v", first, second)
	}
	reader, err := store.Open(context.Background(), first.URI)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, _ := io.ReadAll(reader)
	if string(got) != "same content" {
		t.Fatalf("open content=%q", got)
	}
	if _, err := store.Open(context.Background(), "blob://sha256/../../etc/passwd"); err == nil {
		t.Fatal("path traversal URI should be rejected")
	}
}

func TestLocalPutHonorsCanceledContext(t *testing.T) {
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Put(ctx, strings.NewReader("x")); err == nil {
		t.Fatal("canceled put should fail")
	}
}
