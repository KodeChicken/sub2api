//go:build unit

package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalImageStorageSave(t *testing.T) {
	root := t.TempDir()
	storage, err := NewLocalImageStorage(root)
	require.NoError(t, err)

	gotURL, err := storage.Save(context.Background(), "images/imgtask_123-0.png", "image/png", []byte("png-data"))
	require.NoError(t, err)
	require.Equal(t, "/v1/images/storage/images/imgtask_123-0.png", gotURL)

	data, err := os.ReadFile(filepath.Join(root, "images", "imgtask_123-0.png"))
	require.NoError(t, err)
	require.Equal(t, []byte("png-data"), data)
}

func TestLocalImageStorageRejectsTraversal(t *testing.T) {
	storage, err := NewLocalImageStorage(t.TempDir())
	require.NoError(t, err)

	_, err = storage.Save(context.Background(), "../outside.png", "image/png", []byte("nope"))
	require.ErrorContains(t, err, "invalid local image storage key")
}
