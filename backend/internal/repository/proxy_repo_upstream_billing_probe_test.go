package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestProxyUpdateInvalidatesBoundProbeSnapshotsAndEnqueuesOutboxAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"protocol", "host", "port", "username", "password", "status", "expires_at"}).
			AddRow("http", "old.example", 8080, "user", "pass", service.StatusActive, nil))
	mock.ExpectExec(`(?s)UPDATE "proxies" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProxyUpdateReload(mock, 9, "new.example", "user", "pass")
	mock.ExpectQuery(`(?s)UPDATE accounts.*- 'upstream_billing_probe'.*- 'ollama_cloud_usage_snapshot'.*type = 'apikey'.*extra \? 'upstream_billing_probe'.*platform IN \('openai', 'anthropic', 'kimi', 'zhipu', 'deepseek', 'minimax'\).*extra \? 'ollama_cloud_usage_snapshot'.*RETURNING id`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)).AddRow(int64(18)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM accounts WHERE proxy_id=$1 AND deleted_at IS NULL")).WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
		WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{17, 18}}).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := newProxyRepositoryWithSQL(client, db)
	proxy := &service.Proxy{
		ID:       9,
		Name:     "proxy",
		Protocol: "http",
		Host:     "new.example",
		Port:     8080,
		Username: "user",
		Password: "pass",
		Status:   service.StatusActive,
	}

	err = repo.Update(context.Background(), proxy)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyUpdateRollsBackWhenProbeInvalidationOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"protocol", "host", "port", "username", "password", "status", "expires_at"}).
			AddRow("http", "old.example", 8080, "", "", service.StatusActive, nil))
	mock.ExpectExec(`(?s)UPDATE "proxies" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProxyUpdateReload(mock, 9, "new.example", "", "")
	mock.ExpectQuery(`(?s)UPDATE accounts.*- 'upstream_billing_probe'.*- 'ollama_cloud_usage_snapshot'.*type = 'apikey'.*extra \? 'upstream_billing_probe'.*platform IN \('openai', 'anthropic', 'kimi', 'zhipu', 'deepseek', 'minimax'\).*extra \? 'ollama_cloud_usage_snapshot'.*RETURNING id`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM accounts WHERE proxy_id=$1 AND deleted_at IS NULL")).WithArgs(int64(9)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
		WillReturnError(errors.New("outbox failed"))
	mock.ExpectRollback()

	repo := newProxyRepositoryWithSQL(client, db)
	proxy := &service.Proxy{ID: 9, Name: "proxy", Protocol: "http", Host: "new.example", Port: 8080, Status: service.StatusActive}

	err = repo.Update(context.Background(), proxy)

	require.EqualError(t, err, "outbox failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyUpdateSkipsProbeInvalidationForNonIdentityChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT protocol, host, port") + `.*` + regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"protocol", "host", "port", "username", "password", "status", "expires_at"}).
			AddRow("http", "same.example", 8080, "", "", service.StatusActive, nil))
	mock.ExpectExec(`(?s)UPDATE "proxies" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectProxyUpdateReload(mock, 9, "same.example", "", "")
	mock.ExpectCommit()

	repo := newProxyRepositoryWithSQL(client, db)
	proxy := &service.Proxy{ID: 9, Name: "renamed", Protocol: "http", Host: "same.example", Port: 8080, Status: service.StatusActive}

	err = repo.Update(context.Background(), proxy)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectProxyUpdateReload(mock sqlmock.Sqlmock, id int64, host, username, password string, expiry ...*time.Time) {
	var expiresAt any
	if len(expiry) > 0 && expiry[0] != nil {
		expiresAt = *expiry[0]
	}
	now := time.Now()
	mock.ExpectQuery(`(?s)SELECT .* FROM "proxies" WHERE "id" = \$1`).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "name", "protocol", "host", "port",
			"username", "password", "status", "expires_at", "fallback_mode", "backup_proxy_id", "expiry_warn_days",
		}).AddRow(
			id, now, now, nil, "proxy", "http", host, 8080,
			username, password, service.StatusActive, expiresAt, service.FallbackModeNone, nil, 0,
		))
}

func TestEnqueueProxyAccountChangesChunksLargePayloads(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	accountIDs := make([]int64, 1001)
	for i := range accountIDs {
		accountIDs[i] = int64(i + 1)
	}
	for start := 0; start < len(accountIDs); start += proxyProbeOutboxAccountChunkSize {
		end := start + proxyProbeOutboxAccountChunkSize
		if end > len(accountIDs) {
			end = len(accountIDs)
		}
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
			WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: accountIDs[start:end]}).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}

	err = enqueueProxyProbeAccountChanges(context.Background(), db, accountIDs)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStrictEgressProxyExpiryChangesRefreshAllBindingsAtomically(t *testing.T) {
	for _, scenario := range []string{"renew", "clear expiry", "outbox failure"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
			var nextExpiry *time.Time
			if scenario != "clear expiry" {
				nextExpiry = &future
			}
			mock.ExpectBegin()
			mock.ExpectQuery(`(?s)SELECT protocol, host, port.*status, expires_at.*FOR NO KEY UPDATE`).
				WithArgs(int64(9)).
				WillReturnRows(sqlmock.NewRows([]string{"protocol", "host", "port", "username", "password", "status", "expires_at"}).
					AddRow("http", "same.example", 8080, "", "", service.StatusActive, past))
			mock.ExpectExec(`(?s)UPDATE "proxies" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
			expectProxyUpdateReload(mock, 9, "same.example", "", "", nextExpiry)
			// Expiry-only edits keep billing probe snapshots, but must refresh every
			// account, including OAuth accounts that have no billing snapshot.
			mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM accounts WHERE proxy_id=$1 AND deleted_at IS NULL")).
				WithArgs(int64(9)).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(17)).AddRow(int64(18)))
			outbox := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
				WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{17, 18}})
			if scenario == "outbox failure" {
				outbox.WillReturnError(errors.New("outbox failed"))
				mock.ExpectRollback()
			} else {
				outbox.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}
			repo := newProxyRepositoryWithSQL(client, db)
			proxy := &service.Proxy{ID: 9, Name: "proxy", Protocol: "http", Host: "same.example", Port: 8080, Status: service.StatusActive, ExpiresAt: nextExpiry}
			err = repo.Update(t.Context(), proxy)
			if scenario == "outbox failure" {
				require.EqualError(t, err, "outbox failed")
			} else {
				require.NoError(t, err)
				require.Equal(t, nextExpiry, proxy.ExpiresAt)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
