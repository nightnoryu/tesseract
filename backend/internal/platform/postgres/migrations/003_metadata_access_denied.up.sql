ALTER TABLE tesseract_app.schema_tables DROP CONSTRAINT schema_tables_reason_check;
ALTER TABLE tesseract_app.schema_tables ADD CONSTRAINT schema_tables_reason_check
 CHECK (reason IN ('', 'missing_table', 'missing_column', 'incompatible_type', 'access_denied', 'scan_failed'));
CREATE INDEX schema_scans_successful_lookup ON tesseract_app.schema_scans (source_id, catalog_hash, completed_at DESC) WHERE successful;
