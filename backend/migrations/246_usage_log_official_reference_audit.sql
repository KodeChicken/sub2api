ALTER TABLE usage_logs
ADD COLUMN IF NOT EXISTS official_reference_long_context_enabled BOOLEAN,
ADD COLUMN IF NOT EXISTS official_reference_long_context_applied BOOLEAN,
ADD COLUMN IF NOT EXISTS official_reference_pricing_at TIMESTAMPTZ;

COMMENT ON COLUMN usage_logs.official_reference_long_context_enabled IS
'Request-time long-context policy snapshot used only by catalog reference pricing; NULL means historical state is unknown.';

COMMENT ON COLUMN usage_logs.official_reference_long_context_applied IS
'Whether catalog reference pricing crossed the model long-context threshold; independent from customer long_context_billing_applied.';

COMMENT ON COLUMN usage_logs.official_reference_pricing_at IS
'Exact timestamp used for catalog reference pricing; does not affect customer billing.';
