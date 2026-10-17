-- 0003: convites por link e compartilhamento de dispositivos.

CREATE TABLE IF NOT EXISTS invites (
    id                    BIGSERIAL PRIMARY KEY,
    org_id                BIGINT      NOT NULL,
    code                  VARCHAR(16) NOT NULL UNIQUE,
    created_by            BIGINT      NOT NULL,
    device_id             BIGINT      NOT NULL DEFAULT 0, -- peers.row_id (0 = sem dispositivo)
    session_password_enc  TEXT        NOT NULL DEFAULT '',
    expires_at            TIMESTAMPTZ NOT NULL,
    max_uses              INT         NOT NULL DEFAULT 1,
    uses                  INT         NOT NULL DEFAULT 0,
    revoked_at            TIMESTAMPTZ,
    last_consumed_at      TIMESTAMPTZ,
    last_consumed_ip      TEXT        NOT NULL DEFAULT '',
    note                  TEXT        NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_invites_org    ON invites (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_invites_device ON invites (device_id);

CREATE TABLE IF NOT EXISTS device_shares (
    id           BIGSERIAL PRIMARY KEY,
    org_id       BIGINT      NOT NULL,
    device_id    BIGINT      NOT NULL, -- peers.row_id
    target_type  VARCHAR(8)  NOT NULL, -- user | group
    target_id    BIGINT      NOT NULL,
    level        VARCHAR(8)  NOT NULL DEFAULT 'connect', -- connect | manage
    created_by   BIGINT      NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, target_type, target_id)
);
CREATE INDEX IF NOT EXISTS idx_device_shares_org    ON device_shares (org_id);
CREATE INDEX IF NOT EXISTS idx_device_shares_target ON device_shares (target_type, target_id);
