-- 临时调度支持按时间、按额度、额度或时间三种结束模式。
-- groups 上的字段是请求热路径快照；group_temporary_dispatches 只保存活跃任务。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS temporary_dispatch_mode VARCHAR(16),
    ADD COLUMN IF NOT EXISTS temporary_dispatch_quota_window VARCHAR(2),
    ADD COLUMN IF NOT EXISTS temporary_dispatch_baseline_percent DECIMAL(7,3),
    ADD COLUMN IF NOT EXISTS temporary_dispatch_target_percent DECIMAL(7,3),
    ADD COLUMN IF NOT EXISTS temporary_dispatch_current_percent DECIMAL(7,3),
    ADD COLUMN IF NOT EXISTS temporary_dispatch_quota_reset_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS group_temporary_dispatches (
    dispatch_id VARCHAR(64) PRIMARY KEY,
    account_id BIGINT NOT NULL,
    mode VARCHAR(16) NOT NULL,
    quota_window VARCHAR(2),
    baseline_percent DECIMAL(7,3),
    target_percent DECIMAL(7,3),
    current_percent DECIMAL(7,3),
    quota_reset_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    last_checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_group_temporary_dispatch_mode
        CHECK (mode IN ('time', 'usage', 'hybrid')),
    CONSTRAINT chk_group_temporary_dispatch_window
        CHECK (quota_window IS NULL OR quota_window IN ('5h', '7d')),
    CONSTRAINT chk_group_temporary_dispatch_shape CHECK (
        (mode = 'time' AND quota_window IS NULL AND baseline_percent IS NULL AND target_percent IS NULL AND quota_reset_at IS NULL)
        OR
        (mode IN ('usage', 'hybrid') AND quota_window IS NOT NULL AND baseline_percent IS NOT NULL AND target_percent IS NOT NULL AND quota_reset_at IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_group_temporary_dispatches_due
    ON group_temporary_dispatches (expires_at);

CREATE INDEX IF NOT EXISTS idx_group_temporary_dispatches_quota_stale
    ON group_temporary_dispatches (account_id, last_checked_at)
    WHERE quota_window IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_groups_temporary_dispatch_expiry_cleanup
    ON groups (temporary_dispatch_expires_at)
    WHERE deleted_at IS NULL AND temporary_dispatch_account_id IS NOT NULL;
