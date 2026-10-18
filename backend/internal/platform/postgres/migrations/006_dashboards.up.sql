CREATE TABLE tesseract_app.dashboards (
 id uuid PRIMARY KEY,
 owner uuid NOT NULL REFERENCES tesseract_app.users(id),
 revision bigint NOT NULL CHECK (revision > 0),
 published_revision bigint NOT NULL DEFAULT 0 CHECK (published_revision >= 0 AND published_revision <= revision),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX dashboards_owner ON tesseract_app.dashboards(owner, updated_at DESC, id);
CREATE TABLE tesseract_app.dashboard_revisions (
 dashboard_id uuid NOT NULL REFERENCES tesseract_app.dashboards(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK (revision > 0),
 published boolean NOT NULL,
 spec jsonb NOT NULL CHECK (jsonb_typeof(spec) = 'object' AND octet_length(spec::text) <= 131072),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (dashboard_id, revision)
);
