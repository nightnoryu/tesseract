CREATE TABLE tesseract_app.users (
 id uuid PRIMARY KEY,
 email text NOT NULL UNIQUE CHECK (email = lower(email)),
 disabled boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE tesseract_app.local_credentials (
 user_id uuid PRIMARY KEY REFERENCES tesseract_app.users(id) ON DELETE CASCADE,
 password_hash text NOT NULL
);
CREATE TABLE tesseract_app.sessions (
 token_hash text PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES tesseract_app.users(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user ON tesseract_app.sessions(user_id);
CREATE INDEX sessions_expiry ON tesseract_app.sessions(expires_at);
CREATE TABLE tesseract_app.login_limits (
 key text PRIMARY KEY,
 attempts integer NOT NULL,
 expires_at timestamptz NOT NULL
);
