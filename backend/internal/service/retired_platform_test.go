package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestRetiredKiroAccountsCannotBeCreatedOrUsed(t *testing.T) {
	ctx := context.Background()
	require.False(t, domain.IsGroupPlatform(PlatformKiro))
	require.False(t, (&Group{Platform: PlatformKiro, Status: StatusActive}).IsActive())
	_, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformKiro}, nil)
	require.ErrorIs(t, err, ErrKiroRetired)

	legacy := &Account{Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	require.False(t, legacy.IsSchedulable())
	gateway := &GatewayService{}
	_, _, err = gateway.listSchedulableAccounts(ctx, nil, PlatformKiro, false)
	require.ErrorIs(t, err, ErrKiroRetired)
	_, _, err = gateway.GetAccessToken(ctx, legacy)
	require.ErrorIs(t, err, ErrKiroRetired)
	_, err = (&OAuthService{}).RefreshAccountToken(ctx, legacy)
	require.ErrorIs(t, err, ErrKiroRetired)
}
