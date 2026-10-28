-- Manual phase-1 maintenance only. No schema changes; never alters accounts.proxy_id.
BEGIN;
UPDATE proxies
SET fallback_mode = 'none', backup_proxy_id = NULL, updated_at = NOW()
WHERE fallback_mode <> 'none' OR backup_proxy_id IS NOT NULL;
COMMIT;
