-- 0005: branding/SSO por org, billing (preparação) e fila de jobs.

CREATE TABLE IF NOT EXISTS org_branding (
    org_id         BIGINT PRIMARY KEY,
    product_name   TEXT        NOT NULL DEFAULT '',
    logo_url       TEXT        NOT NULL DEFAULT '',
    primary_color  TEXT        NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS org_sso (
    org_id             BIGINT PRIMARY KEY,
    enabled            BOOLEAN     NOT NULL DEFAULT false,
    issuer             TEXT        NOT NULL DEFAULT '',
    client_id          TEXT        NOT NULL DEFAULT '',
    client_secret_enc  TEXT        NOT NULL DEFAULT '',
    scopes             TEXT        NOT NULL DEFAULT 'openid email profile',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plans (
    id               BIGSERIAL PRIMARY KEY,
    code             TEXT    NOT NULL UNIQUE,
    name             TEXT    NOT NULL,
    max_devices      INT     NOT NULL DEFAULT 0, -- 0 = ilimitado
    max_techs        INT     NOT NULL DEFAULT 0,
    price_cents      INT     NOT NULL DEFAULT 0,
    stripe_price_id  TEXT    NOT NULL DEFAULT ''
);
INSERT INTO plans (code, name, max_devices, max_techs, price_cents) VALUES
    ('free',     'Free',     3,   3, 0),
    ('pro',      'Pro',      50,  5, 9900),
    ('business', 'Business', 0,   0, 29900)
ON CONFLICT (code) DO NOTHING;

CREATE TABLE IF NOT EXISTS subscriptions (
    id                      BIGSERIAL PRIMARY KEY,
    org_id                  BIGINT      NOT NULL UNIQUE,
    plan_id                 BIGINT      NOT NULL,
    status                  TEXT        NOT NULL DEFAULT 'active', -- active|trialing|past_due|canceled
    stripe_customer_id      TEXT        NOT NULL DEFAULT '',
    stripe_subscription_id  TEXT        NOT NULL DEFAULT '',
    current_period_end      TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_subscriptions_customer ON subscriptions (stripe_customer_id);

CREATE TABLE IF NOT EXISTS stripe_events (
    id            TEXT PRIMARY KEY,
    type          TEXT        NOT NULL,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS jobs (
    id          BIGSERIAL PRIMARY KEY,
    org_id      BIGINT      NOT NULL DEFAULT 0,
    type        TEXT        NOT NULL,
    dedupe_key  TEXT        NOT NULL DEFAULT '',
    payload     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    status      TEXT        NOT NULL DEFAULT 'pending', -- pending|running|done|failed
    attempts    INT         NOT NULL DEFAULT 0,
    last_error  TEXT        NOT NULL DEFAULT '',
    result      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    run_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs (status, run_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_dedupe ON jobs (type, dedupe_key) WHERE dedupe_key <> '' AND status IN ('pending', 'running');
