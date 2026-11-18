package dto

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupFromServiceAdminIncludesTemporaryDispatch(t *testing.T) {
	accountID := int64(19)
	startedAt := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	expiresAt := startedAt.Add(time.Hour)
	group := &service.Group{
		TemporaryDispatchAccountID:        &accountID,
		TemporaryDispatchAccountIDs:       []int64{accountID},
		TemporaryDispatchAccountDeadlines: map[string]time.Time{"19": expiresAt},
		TemporaryDispatchID:               "td_test",
		TemporaryDispatchStartedAt:        &startedAt,
		TemporaryDispatchExpiresAt:        &expiresAt,
		TemporaryDispatchMode:             service.TemporaryDispatchModeTime,
		TemporaryDispatchUsageMetric:      service.TemporaryDispatchUsageQuotaPercent,
		TemporaryDispatchQuotaWindow:      service.TemporaryDispatchQuotaWindow7d,
	}

	admin := GroupFromServiceAdmin(group)
	require.Equal(t, accountID, *admin.TemporaryDispatchAccountID)
	require.Equal(t, []int64{accountID}, admin.TemporaryDispatchAccountIDs)
	require.Equal(t, expiresAt, admin.TemporaryDispatchAccountDeadlines["19"])
	require.Equal(t, "td_test", admin.TemporaryDispatchID)
	require.Equal(t, startedAt, *admin.TemporaryDispatchStartedAt)
	require.Equal(t, expiresAt, *admin.TemporaryDispatchExpiresAt)
	require.Equal(t, service.TemporaryDispatchModeTime, admin.TemporaryDispatchMode)
	require.Equal(t, service.TemporaryDispatchUsageQuotaPercent, admin.TemporaryDispatchUsageMetric)
	require.Equal(t, service.TemporaryDispatchQuotaWindow7d, admin.TemporaryDispatchQuotaWindow)
}
