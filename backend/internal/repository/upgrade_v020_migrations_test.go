//go:build unit

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// The workflow supplies a disposable PostgreSQL service and the exact baseline
// migration tree. Each case uses a separate database, including public schema
// references made by historical migrations. No application config is loaded.
func TestUpgradeV020Migrations(t *testing.T) {
	dsn := os.Getenv("UPGRADE_TEST_DSN")
	baselinePath := os.Getenv("UPGRADE_BASELINE_MIGRATIONS")
	if dsn == "" || baselinePath == "" {
		t.Skip("requires disposable upgrade workflow database and baseline migrations")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	base, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = base.Close() }()
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom_baseline_%t", upgrade), func(t *testing.T) {
			name := fmt.Sprintf("upgrade_v020_%d", time.Now().UnixNano())
			_, err := base.ExecContext(ctx, "CREATE DATABASE "+name)
			require.NoError(t, err)
			db, err := sql.Open("postgres", dsn+" dbname="+name)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = db.Close()
				_, _ = base.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
			})
			if upgrade {
				require.NoError(t, applyMigrationsFS(ctx, db, os.DirFS(baselinePath)))
				_, err = db.ExecContext(ctx, `
INSERT INTO users (id,email,password_hash) VALUES (901,'upgrade@example.invalid','fixture');
INSERT INTO proxies (id,name,protocol,host,port) VALUES (902,'upgrade-proxy','http','proxy.invalid',8080);
INSERT INTO channel_monitors (id,name,provider,endpoint,api_key_encrypted,primary_model,interval_seconds,created_by,auto_check_enabled,visibility,proxy_id,sort_order)
VALUES (903,'upgrade-monitor','openai','https://upstream.invalid','fixture-encrypted','fixture-model',60,901,false,'selected',902,17);
INSERT INTO channel_monitor_users(monitor_id,user_id) VALUES(903,901);`)
				require.NoError(t, err)
			}
			for restart := 0; restart < 3; restart++ {
				require.NoError(t, ApplyMigrations(ctx, db), "startup %d", restart)
				for _, file := range []string{"249_channel_monitor_manual_access.sql", "249_mcp_oauth.sql", "250_channel_monitor_sort_order.sql"} {
					content, err := fs.ReadFile(migrations.FS, file)
					require.NoError(t, err)
					digest := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
					var checksum string
					require.NoError(t, db.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename=$1", file).Scan(&checksum))
					require.Equal(t, hex.EncodeToString(digest[:]), checksum)
				}
				var grants int
				require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM mcp_oauth_grants").Scan(&grants))
				require.Zero(t, grants)
				if upgrade {
					var name, visibility, endpoint, secret, model string
					var auto bool
					var proxy, order, viewers int
					require.NoError(t, db.QueryRowContext(ctx, `SELECT name,visibility,endpoint,api_key_encrypted,primary_model,auto_check_enabled,proxy_id,sort_order FROM channel_monitors WHERE id=903`).Scan(&name,&visibility,&endpoint,&secret,&model,&auto,&proxy,&order))
					require.Equal(t, "upgrade-monitor", name)
					require.Equal(t, "selected", visibility)
					require.Equal(t, "https://upstream.invalid", endpoint)
					require.Equal(t, "fixture-encrypted", secret)
					require.Equal(t, "fixture-model", model)
					require.False(t, auto)
					require.Equal(t, 902, proxy)
					require.Equal(t, 17, order)
					require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM channel_monitor_users WHERE monitor_id=903 AND user_id=901").Scan(&viewers))
					require.Equal(t, 1, viewers)
				}
			}
		})
	}
}
