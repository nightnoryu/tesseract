CREATE TABLE tesseract_app.dashboard_chat (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 dashboard_id uuid NOT NULL REFERENCES tesseract_app.dashboards(id) ON DELETE CASCADE,
 revision bigint NOT NULL,
 kind text NOT NULL CHECK (kind IN ('create', 'edit', 'message', 'discard')),
 entries jsonb NOT NULL CHECK (jsonb_typeof(entries) = 'array' AND octet_length(entries::text) <= 262144),
 operations jsonb NOT NULL CHECK (jsonb_typeof(operations) = 'array' AND octet_length(operations::text) <= 131072),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (dashboard_id, revision) REFERENCES tesseract_app.dashboard_revisions(dashboard_id, revision) ON DELETE CASCADE
);
CREATE INDEX dashboard_chat_history ON tesseract_app.dashboard_chat(dashboard_id, id DESC);
