ALTER TABLE usage_logs
ADD COLUMN IF NOT EXISTS official_reference_cost NUMERIC(20,10);

COMMENT ON COLUMN usage_logs.official_reference_cost IS
'Catalog/fallback reference price for the request, excluding customer channel/group prices and multipliers.';
