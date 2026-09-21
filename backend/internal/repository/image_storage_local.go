package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const localImageStorageURLPrefix = "/v1/images/storage"

// LocalImageStorage stores asynchronous image results on the server filesystem.
type LocalImageStorage struct {
	root string
}

var _ service.ImageStorage = (*LocalImageStorage)(nil)

func NewLocalImageStorage(root string) (*LocalImageStorage, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("local image storage directory is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve local image storage directory: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create local image storage directory: %w", err)
	}
	return &LocalImageStorage{root: absRoot}, nil
}

func (s *LocalImageStorage) Save(ctx context.Context, key, _ string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	relative, err := safeLocalImagePath(key)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(s.root, relative)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", fmt.Errorf("create local image directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".image-*")
	if err != nil {
		return "", fmt.Errorf("create local image temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("set local image permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write local image: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close local image: %w", err)
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return "", fmt.Errorf("publish local image: %w", err)
	}
	return localImageStorageURLPrefix + "/" + escapeURLPath(filepath.ToSlash(relative)), nil
}

func safeLocalImagePath(key string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(key)))
	if cleaned == "." || filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid local image storage key")
	}
	return cleaned, nil
}

func escapeURLPath(value string) string {
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
