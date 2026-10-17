-- 0002: refresh tokens rotativos e reset de senha.

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT      NOT NULL,
    token_hash   TEXT        NOT NULL UNIQUE,
    family       TEXT        NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ,
    replaced_by  BIGINT      NOT NULL DEFAULT 0,
    ip           TEXT        NOT NULL DEFAULT '',
    user_agent   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user   ON refresh_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family ON refresh_tokens (family);

CREATE TABLE IF NOT EXISTS password_resets (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT      NOT NULL,
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_password_resets_user ON password_resets (user_id);
