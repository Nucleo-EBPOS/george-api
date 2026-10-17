-- 0001: organizações e org_id nas tabelas do upstream.
-- Pré-requisito: o AutoMigrate do upstream (users, peers, groups, tags, address_books, ...)
-- já rodou; aqui só acrescentamos colunas/tabelas do George.

CREATE TABLE IF NOT EXISTS orgs (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL,
    slug        TEXT        NOT NULL UNIQUE,
    status      INT         NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users ADD COLUMN IF NOT EXISTS org_id        BIGINT      NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS role          VARCHAR(16) NOT NULL DEFAULT 'tech';
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_ops        BOOLEAN     NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret   TEXT        NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled  BOOLEAN     NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_users_org_id ON users (org_id);

ALTER TABLE peers ADD COLUMN IF NOT EXISTS org_id        BIGINT  NOT NULL DEFAULT 0;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS host_enabled  BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS platform      TEXT    NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_peers_org_id ON peers (org_id);

ALTER TABLE groups                   ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE device_groups            ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE tags                     ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE address_books            ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE address_book_collections ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_groups_org_id        ON groups (org_id);
CREATE INDEX IF NOT EXISTS idx_device_groups_org_id ON device_groups (org_id);
CREATE INDEX IF NOT EXISTS idx_tags_org_id          ON tags (org_id);
CREATE INDEX IF NOT EXISTS idx_address_books_org_id ON address_books (org_id);
