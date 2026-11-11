-- Run once as the database owner. This is an external adapter, not an Ent migration.
-- Only this view needs updating if upstream renames the proxy/account columns.
CREATE SCHEMA IF NOT EXISTS network_status;
CREATE OR REPLACE VIEW network_status.inventory WITH (security_barrier = true) AS
SELECT 0::bigint AS id, '服务器直连'::text AS name, 'direct'::text AS protocol,
       ''::text AS host, 0 AS port, ''::text AS username, ''::text AS password,
       'active'::text AS status, NULL::double precision AS expires_at, false AS deleted,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'name',a.name) ORDER BY a.id)
                 FROM accounts a WHERE a.deleted_at IS NULL AND (a.proxy_id IS NULL OR a.proxy_id=0)), '[]'::jsonb) AS accounts
UNION ALL
SELECT p.id, p.name, p.protocol, p.host, p.port, COALESCE(p.username,''), COALESCE(p.password,''),
       p.status, EXTRACT(EPOCH FROM p.expires_at)::double precision, p.deleted_at IS NOT NULL,
       jsonb_agg(jsonb_build_object('id',a.id,'name',a.name) ORDER BY a.id)
FROM proxies p JOIN accounts a ON a.proxy_id=p.id AND a.deleted_at IS NULL
GROUP BY p.id;

DO $$ BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='sub2api_network_reader') THEN
        CREATE ROLE sub2api_network_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
    END IF;
END $$;
GRANT USAGE ON SCHEMA network_status TO sub2api_network_reader;
GRANT SELECT ON network_status.inventory TO sub2api_network_reader;
SELECT format('GRANT CONNECT ON DATABASE %I TO sub2api_network_reader', current_database()) \gexec
ALTER ROLE sub2api_network_reader SET default_transaction_read_only=on;
ALTER ROLE sub2api_network_reader SET statement_timeout='5s';
