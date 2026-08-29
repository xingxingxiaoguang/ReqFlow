// Package blobstore 提供内容寻址 BlobStore 的基础设施实现。
package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"reqflow/internal/port"
)

const localURIPrefix = "blob://sha256/"

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Local 把 Blob 按 sha256 前缀分片保存在一个受控根目录中。
// URI 不暴露物理路径，使仓储记录可在切换 S3/MinIO 时保持同一抽象。
type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("blobstore: local root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("blobstore: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("blobstore: create root: %w", err)
	}
	return &Local{root: abs}, nil
}

func (s *Local) Put(ctx context.Context, content io.Reader) (port.BlobObject, error) {
	if content == nil {
		return port.BlobObject{}, fmt.Errorf("blobstore: content is required")
	}
	tmp, err := os.CreateTemp(s.root, ".incoming-*")
	if err != nil {
		return port.BlobObject{}, fmt.Errorf("blobstore: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), contextReader{ctx: ctx, reader: content})
	if err != nil {
		return port.BlobObject{}, fmt.Errorf("blobstore: write content: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return port.BlobObject{}, fmt.Errorf("blobstore: sync content: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return port.BlobObject{}, fmt.Errorf("blobstore: close content: %w", err)
	}

	digest := hex.EncodeToString(hash.Sum(nil))
	dir := filepath.Join(s.root, digest[:2])
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return port.BlobObject{}, fmt.Errorf("blobstore: create shard: %w", err)
	}
	target := filepath.Join(dir, digest)
	// Link 在同一文件系统内提供 create-if-absent；并发上传相同内容时只有一个
	// 调用创建目标，其余调用复用已有 Blob，永不覆盖已存在内容。
	if err := os.Link(tmpPath, target); err != nil && !os.IsExist(err) {
		return port.BlobObject{}, fmt.Errorf("blobstore: publish content: %w", err)
	}
	removeTemp = true
	return port.BlobObject{URI: localURIPrefix + digest, SHA256: digest, SizeBytes: size}, nil
}

func (s *Local) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	digest, err := digestFromURI(uri)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(s.root, digest[:2], digest))
	if err != nil {
		return nil, fmt.Errorf("blobstore: open %s: %w", uri, err)
	}
	return file, nil
}

func digestFromURI(uri string) (string, error) {
	if !strings.HasPrefix(uri, localURIPrefix) {
		return "", fmt.Errorf("blobstore: unsupported URI %q", uri)
	}
	digest := strings.TrimPrefix(uri, localURIPrefix)
	if !sha256Pattern.MatchString(digest) {
		return "", fmt.Errorf("blobstore: invalid sha256 URI")
	}
	return digest, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}
