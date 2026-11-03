package kiro

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// OAuthSessionStore keeps Kiro secrets server-side; Swap also fences expired claims.
type OAuthSessionStore interface {
	Read(context.Context, string) ([]byte, time.Time, error)
	Swap(context.Context, string, []byte, []byte, time.Time, time.Duration) (bool, error)
}

type redisOAuthSessions struct{ client *redis.Client }

func NewOAuthSessionStore(client *redis.Client) OAuthSessionStore {
	return &redisOAuthSessions{client: client}
}

func (s *redisOAuthSessions) Read(ctx context.Context, id string) ([]byte, time.Time, error) {
	if s.client == nil {
		return nil, time.Time{}, errors.New("Kiro Redis sessions unavailable")
	}
	pipe := s.client.Pipeline()
	data := pipe.Get(ctx, "oauth:session:kiro:"+id)
	now := pipe.Time(ctx)
	_, err := pipe.Exec(ctx)
	if (err != nil && !errors.Is(err, redis.Nil)) || now.Err() != nil {
		return nil, time.Time{}, errors.New("Kiro Redis sessions unavailable")
	}
	return []byte(data.Val()), now.Val(), nil
}

var swapOAuthSession = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
if tonumber(ARGV[3]) > 0 and now >= tonumber(ARGV[3]) then return 0 end
local old = redis.call('GET', KEYS[1])
if ARGV[1] == '' then
  if old then return 0 end
elseif old ~= ARGV[1] then return 0 end
redis.call('PSETEX', KEYS[1], ARGV[4], ARGV[2])
return 1
`)

func (s *redisOAuthSessions) Swap(ctx context.Context, id string, before, after []byte, deadline time.Time, ttl time.Duration) (bool, error) {
	if s.client == nil {
		return false, errors.New("Kiro Redis sessions unavailable")
	}
	until := int64(0)
	if !deadline.IsZero() {
		until = deadline.UnixMilli()
	}
	ok, err := swapOAuthSession.Run(ctx, s.client, []string{"oauth:session:kiro:" + id},
		string(before), string(after), until, max(ttl.Milliseconds(), 1)).Int()
	if err != nil {
		return false, errors.New("Kiro Redis sessions unavailable")
	}
	return ok == 1, nil
}
