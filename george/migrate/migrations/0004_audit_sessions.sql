-- 0004: auditoria e sessões (ingestão dos eventos audit/conn do hbbs/cliente).

CREATE TABLE IF NOT EXISTS audit_logs (
    id           BIGSERIAL PRIMARY KEY,
    org_id       BIGINT      NOT NULL,
    actor_id     BIGINT      NOT NULL DEFAULT 0,
    actor_name   TEXT        NOT NULL DEFAULT '',
    action       TEXT        NOT NULL,
    target_type  TEXT        NOT NULL DEFAULT '',
    target_id    TEXT        NOT NULL DEFAULT '',
    ip           TEXT        NOT NULL DEFAULT '',
    user_agent   TEXT        NOT NULL DEFAULT '',
    request_id   TEXT        NOT NULL DEFAULT '',
    meta         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_org ON audit_logs (org_id, created_at DESC);

CREATE TABLE IF NOT EXISTS sessions (
    id             BIGSERIAL PRIMARY KEY,
    org_id         BIGINT      NOT NULL DEFAULT 0,
    device_id      BIGINT      NOT NULL DEFAULT 0, -- peers.row_id
    peer_id        TEXT        NOT NULL DEFAULT '', -- id RustDesk do host
    from_peer      TEXT        NOT NULL DEFAULT '',
    from_name      TEXT        NOT NULL DEFAULT '',
    from_user_id   BIGINT      NOT NULL DEFAULT 0,
    conn_id        BIGINT      NOT NULL DEFAULT 0,
    session_uuid   TEXT        NOT NULL DEFAULT '',
    audit_conn_id  BIGINT      NOT NULL DEFAULT 0 UNIQUE,
    conn_type      INT         NOT NULL DEFAULT 0,
    ip             TEXT        NOT NULL DEFAULT '',
    started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at       TIMESTAMPTZ,
    bytes          BIGINT      NOT NULL DEFAULT 0,
    recording_ref  TEXT        NOT NULL DEFAULT '',
    summary        TEXT        NOT NULL DEFAULT '',
    summary_status TEXT        NOT NULL DEFAULT '', -- '' | pending | done | failed
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_sessions_org    ON sessions (org_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_device ON sessions (device_id);
