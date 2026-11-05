-- Preserve existing schedules and visibility; new service/API records default to manual/admin-only.
ALTER TABLE channel_monitors ADD COLUMN auto_check_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE channel_monitors ALTER COLUMN auto_check_enabled SET DEFAULT FALSE;
ALTER TABLE channel_monitors ADD COLUMN visibility VARCHAR(20) NOT NULL DEFAULT 'all'
    CHECK (visibility IN ('all', 'selected', 'admin_only'));
ALTER TABLE channel_monitors ADD COLUMN proxy_id BIGINT REFERENCES proxies(id) ON DELETE SET NULL;
CREATE TABLE channel_monitor_users (
    monitor_id BIGINT NOT NULL REFERENCES channel_monitors(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (monitor_id, user_id)
);
CREATE INDEX idx_channel_monitor_users_user ON channel_monitor_users(user_id, monitor_id);
