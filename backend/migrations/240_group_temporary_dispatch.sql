-- 临时账号接管只叠加调度覆盖，不修改分组原有 account_groups 绑定。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS temporary_dispatch_account_id BIGINT,
    ADD COLUMN IF NOT EXISTS temporary_dispatch_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS temporary_dispatch_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS temporary_dispatch_expires_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_groups_temporary_dispatch_active
    ON groups (temporary_dispatch_account_id, temporary_dispatch_expires_at)
    WHERE deleted_at IS NULL AND temporary_dispatch_account_id IS NOT NULL;
