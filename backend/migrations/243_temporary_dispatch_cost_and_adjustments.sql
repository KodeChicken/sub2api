-- 临时调度用量口径：OpenAI OAuth 使用配额百分比，API Key/上游账号使用账号成本。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS temporary_dispatch_usage_metric VARCHAR(24);

ALTER TABLE group_temporary_dispatches
    ADD COLUMN IF NOT EXISTS usage_metric VARCHAR(24);

ALTER TABLE group_temporary_dispatch_accounts
    ADD COLUMN IF NOT EXISTS usage_metric VARCHAR(24);

-- 百分比原本只需 0~100；复用这些列保存账号成本后扩大精度，避免较大
-- 上游消耗目标（例如数万美元）溢出，同时保留足够的小数位。
ALTER TABLE groups
    ALTER COLUMN temporary_dispatch_baseline_percent TYPE DECIMAL(18,6),
    ALTER COLUMN temporary_dispatch_target_percent TYPE DECIMAL(18,6),
    ALTER COLUMN temporary_dispatch_current_percent TYPE DECIMAL(18,6);

ALTER TABLE group_temporary_dispatches
    ALTER COLUMN baseline_percent TYPE DECIMAL(18,6),
    ALTER COLUMN target_percent TYPE DECIMAL(18,6),
    ALTER COLUMN current_percent TYPE DECIMAL(18,6);

ALTER TABLE group_temporary_dispatch_accounts
    ALTER COLUMN baseline_percent TYPE DECIMAL(18,6),
    ALTER COLUMN target_percent TYPE DECIMAL(18,6),
    ALTER COLUMN current_percent TYPE DECIMAL(18,6);

UPDATE group_temporary_dispatches
SET usage_metric = 'quota_percent'
WHERE quota_window IS NOT NULL AND usage_metric IS NULL;

UPDATE group_temporary_dispatch_accounts
SET usage_metric = 'quota_percent'
WHERE quota_window IS NOT NULL AND usage_metric IS NULL;

UPDATE groups
SET temporary_dispatch_usage_metric = 'quota_percent'
WHERE temporary_dispatch_quota_window IS NOT NULL
  AND temporary_dispatch_usage_metric IS NULL;

ALTER TABLE group_temporary_dispatches
    DROP CONSTRAINT IF EXISTS chk_group_temporary_dispatch_usage_metric,
    ADD CONSTRAINT chk_group_temporary_dispatch_usage_metric
        CHECK (usage_metric IS NULL OR usage_metric IN ('quota_percent', 'account_cost'));

ALTER TABLE group_temporary_dispatch_accounts
    DROP CONSTRAINT IF EXISTS chk_group_temporary_dispatch_account_usage_metric,
    ADD CONSTRAINT chk_group_temporary_dispatch_account_usage_metric
        CHECK (usage_metric IS NULL OR usage_metric IN ('quota_percent', 'account_cost'));

CREATE INDEX IF NOT EXISTS idx_group_temporary_dispatch_accounts_cost_active
    ON group_temporary_dispatch_accounts (account_id, created_at)
    WHERE usage_metric = 'account_cost';
