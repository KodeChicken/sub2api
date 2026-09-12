-- 239: 同步拼车账号额度重置到拼车订阅，并支持审计/安全撤回。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS is_carpool BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS quota_reset_revision BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS carpool_subscription_quota_reset_events (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source VARCHAR(40) NOT NULL,
    event_key VARCHAR(160) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'applied',
    affected_count INTEGER NOT NULL DEFAULT 0,
    snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    undone_at TIMESTAMPTZ,
    UNIQUE (account_id, source, event_key)
);

CREATE INDEX IF NOT EXISTS idx_carpool_quota_reset_events_latest
    ON carpool_subscription_quota_reset_events (account_id, id DESC);

COMMENT ON TABLE carpool_subscription_quota_reset_events IS
    '拼车账号额度重置订阅的审计记录及撤回快照';
COMMENT ON COLUMN groups.is_carpool IS
    '订阅拼车分组：关联账号额度重置时同步重置组内订阅配额';
COMMENT ON COLUMN user_subscriptions.quota_reset_revision IS
    '配额窗口重置版本，用于拒绝覆盖后续重置的撤回操作';
