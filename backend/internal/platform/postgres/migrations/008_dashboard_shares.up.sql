CREATE TABLE tesseract_app.dashboard_shares (
 dashboard_id uuid NOT NULL REFERENCES tesseract_app.dashboards(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES tesseract_app.users(id) ON DELETE CASCADE,
 PRIMARY KEY (dashboard_id, user_id)
);
CREATE INDEX dashboard_shares_user ON tesseract_app.dashboard_shares(user_id);
