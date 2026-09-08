-- Seller login accounts, one per provider (self-service registration creates
-- both a providers row and a users row together — see internal/auth).
-- zitadel_subject is the "sub" claim from the Zitadel-issued access token
-- presented on every authenticated dashboard request; it's how a request is
-- resolved to the provider_id it's scoped to.

CREATE TABLE users (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    zitadel_subject TEXT        NOT NULL UNIQUE,
    email           TEXT        NOT NULL,

    provider_id     TEXT        NOT NULL,
    bpp_id          TEXT        NOT NULL,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (provider_id, bpp_id),
    FOREIGN KEY (provider_id, bpp_id) REFERENCES providers (id, bpp_id)
);

COMMENT ON TABLE users IS
    'Seller login accounts. One user owns exactly one provider (self-service registration).';

COMMENT ON COLUMN users.zitadel_subject IS
    'The "sub" claim from Zitadel-issued tokens — resolves an authenticated request to its provider_id.';

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

---- create above / drop below ----

DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
DROP TABLE IF EXISTS users;
