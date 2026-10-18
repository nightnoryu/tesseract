CREATE TABLE tesseract_app.oidc_identities (
 issuer text NOT NULL,
 subject text NOT NULL,
 user_id uuid NOT NULL REFERENCES tesseract_app.users(id) ON DELETE CASCADE,
 PRIMARY KEY (issuer, subject),
 UNIQUE (user_id, issuer)
);
CREATE TABLE tesseract_app.oidc_attempts (
 state_hash text PRIMARY KEY,
 browser_hash text NOT NULL,
 nonce text NOT NULL,
 verifier text NOT NULL,
 old_session_hash text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE INDEX oidc_attempts_expiry ON tesseract_app.oidc_attempts(expires_at);
