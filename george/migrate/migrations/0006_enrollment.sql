-- 0006: enrollment de PCs (códigos de vínculo + dispositivos da org com acesso não-assistido).
-- Spec: docs/specs/06-enrollment.md §2.5.

CREATE TABLE IF NOT EXISTS enrollment_keys (
    id          BIGSERIAL PRIMARY KEY,
    org_id      BIGINT      NOT NULL,
    code        TEXT        NOT NULL UNIQUE,
    label       TEXT        NOT NULL DEFAULT '',
    group_id    BIGINT      NOT NULL DEFAULT 0,
    max_uses    INT         NOT NULL DEFAULT 0,
    uses        INT         NOT NULL DEFAULT 0,
    expires_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    created_by  BIGINT      NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_enrollment_keys_org_id ON enrollment_keys (org_id);

ALTER TABLE peers ADD COLUMN IF NOT EXISTS enrolled_at        TIMESTAMPTZ;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS enrollment_key_id  BIGINT;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS device_token_hash  TEXT;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS pw_enc             TEXT;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS pw_version         INT     NOT NULL DEFAULT 0;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS pw_applied_version INT     NOT NULL DEFAULT 0;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS unattended         BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS revoked_at         TIMESTAMPTZ;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS last_agent_at      BIGINT  NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS peers_device_token_hash ON peers (device_token_hash) WHERE device_token_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_peers_enrolled ON peers (org_id) WHERE enrolled_at IS NOT NULL;
