package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuthSnapshotPreservesImageCountBillingAndTemporaryDispatch(t *testing.T) {
	svc := &APIKeyService{}
	deadline := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
	key := &APIKey{
		User: &User{ID: 1},
		Group: &Group{
			ImageCountBilling:                 true,
			TemporaryDispatchAccountIDs:       []int64{17, 23},
			TemporaryDispatchAccountDeadlines: map[string]time.Time{"17": deadline},
		},
	}
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var cached APIKeyAuthSnapshot
	require.NoError(t, json.Unmarshal(data, &cached))
	restored := svc.snapshotToAPIKey("key", &cached)
	require.True(t, restored.Group.ImageCountBilling)
	require.Equal(t, key.Group.TemporaryDispatchAccountIDs, restored.Group.TemporaryDispatchAccountIDs)
	require.Equal(t, key.Group.TemporaryDispatchAccountDeadlines, restored.Group.TemporaryDispatchAccountDeadlines)

	cached.Version = 25
	_, accepted, err := svc.applyAuthCacheEntry("key", &APIKeyAuthCacheEntry{Snapshot: &cached})
	require.NoError(t, err)
	require.False(t, accepted, "v25 predates image count billing in this fork")
}
