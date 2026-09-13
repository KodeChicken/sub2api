-- 临时调度账号池：每个目标账号独立保存时间与额度结束条件。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS temporary_dispatch_account_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS temporary_dispatch_account_deadlines JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS group_temporary_dispatch_accounts (
    dispatch_id VARCHAR(64) NOT NULL REFERENCES group_temporary_dispatches(dispatch_id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    quota_window VARCHAR(2),
    baseline_percent DECIMAL(7,3),
    target_percent DECIMAL(7,3),
    current_percent DECIMAL(7,3),
    quota_reset_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    last_checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dispatch_id, account_id),
    CONSTRAINT chk_group_temporary_dispatch_account_window
        CHECK (quota_window IS NULL OR quota_window IN ('5h', '7d')),
    CONSTRAINT chk_group_temporary_dispatch_account_quota_shape CHECK (
        (quota_window IS NULL AND baseline_percent IS NULL AND target_percent IS NULL AND quota_reset_at IS NULL)
        OR
        (quota_window IS NOT NULL AND baseline_percent IS NOT NULL AND target_percent IS NOT NULL AND quota_reset_at IS NOT NULL)
    )
);

-- 将升级前仍活跃的单账号任务迁移为一个成员的账号池。
INSERT INTO group_temporary_dispatch_accounts (
    dispatch_id, account_id, position, quota_window, baseline_percent,
    target_percent, current_percent, quota_reset_at, expires_at,
    last_checked_at, created_at
)
SELECT dispatch_id, account_id, 0, quota_window, baseline_percent,
       target_percent, current_percent, quota_reset_at, expires_at,
       last_checked_at, created_at
FROM group_temporary_dispatches
ON CONFLICT (dispatch_id, account_id) DO NOTHING;

UPDATE groups
SET temporary_dispatch_account_ids = jsonb_build_array(temporary_dispatch_account_id),
    temporary_dispatch_account_deadlines = CASE
        WHEN temporary_dispatch_expires_at IS NOT NULL THEN jsonb_build_object(
            temporary_dispatch_account_id::text,
            temporary_dispatch_expires_at
        )
        ELSE '{}'::jsonb
    END
WHERE temporary_dispatch_account_id IS NOT NULL
  AND temporary_dispatch_account_ids = '[]'::jsonb;

CREATE INDEX IF NOT EXISTS idx_group_temporary_dispatch_accounts_due
    ON group_temporary_dispatch_accounts (expires_at);

CREATE INDEX IF NOT EXISTS idx_group_temporary_dispatch_accounts_quota_stale
    ON group_temporary_dispatch_accounts (account_id, last_checked_at)
    WHERE quota_window IS NOT NULL;
