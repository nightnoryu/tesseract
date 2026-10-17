-- Synthetic local fixture only. Run explicitly in an empty development DWH.
CREATE SCHEMA mart;
GO
CREATE TABLE mart.MarketingLeads (
    LeadId nvarchar(64) NOT NULL PRIMARY KEY,
    CreatedAt datetime2 NOT NULL,
    ChannelName nvarchar(100) NULL
);
INSERT INTO mart.MarketingLeads (LeadId, CreatedAt, ChannelName) VALUES
    ('demo-1', '2026-01-05T09:00:00', 'Organic'),
    ('demo-2', '2026-01-12T10:00:00', 'Paid search'),
    ('demo-3', '2026-02-03T11:00:00', 'Organic'),
    ('demo-4', '2026-02-04T12:00:00', NULL);
