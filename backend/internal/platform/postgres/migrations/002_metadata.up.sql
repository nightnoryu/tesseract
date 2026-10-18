CREATE TABLE tesseract_app.schema_scans (
 id uuid PRIMARY KEY,
 source_id text NOT NULL,
 catalog_hash text NOT NULL,
 started_at timestamptz NOT NULL,
 completed_at timestamptz NOT NULL,
 successful boolean NOT NULL,
 UNIQUE (id, source_id, catalog_hash)
);
CREATE TABLE tesseract_app.schema_tables (
 scan_id uuid NOT NULL REFERENCES tesseract_app.schema_scans(id) ON DELETE CASCADE,
 dataset_id text NOT NULL,
 schema_name text NOT NULL,
 table_name text NOT NULL,
 present boolean NOT NULL,
 reason text NOT NULL CHECK (reason IN ('', 'missing_table', 'missing_column', 'incompatible_type', 'scan_failed')),
 inspected_at timestamptz NOT NULL,
 PRIMARY KEY (scan_id, dataset_id)
);
CREATE TABLE tesseract_app.schema_columns (
 scan_id uuid NOT NULL,
 dataset_id text NOT NULL,
 column_id text NOT NULL,
 column_name text NOT NULL,
 expected_type text NOT NULL,
 present boolean NOT NULL,
 actual_type text NOT NULL,
 nullable boolean NOT NULL,
 max_length integer NOT NULL,
 numeric_precision integer NOT NULL,
 numeric_scale integer NOT NULL,
 reason text NOT NULL,
 PRIMARY KEY (scan_id, dataset_id, column_id),
 FOREIGN KEY (scan_id, dataset_id) REFERENCES tesseract_app.schema_tables(scan_id, dataset_id) ON DELETE CASCADE
);
CREATE TABLE tesseract_app.schema_current (
 source_id text NOT NULL,
 catalog_hash text NOT NULL,
 scan_id uuid NOT NULL,
 PRIMARY KEY (source_id, catalog_hash),
 FOREIGN KEY (scan_id, source_id, catalog_hash) REFERENCES tesseract_app.schema_scans(id, source_id, catalog_hash)
);
