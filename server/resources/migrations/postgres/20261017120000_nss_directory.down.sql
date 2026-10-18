-- Rollback: drops the NSS directory tables. Every stored POSIX account,
-- group and membership is lost, and so is every fleet key and its usage;
-- hosts lose name resolution through the server until the tables are
-- recreated and the sync refills them, and every key must be issued again.
-- The links from audit events to the rows they concern go too; the events
-- themselves stay.
-- Children first, so nothing is dropped while a surviving table still
-- REFERENCEs it; each table takes its indexes with it.
DROP TABLE IF EXISTS nss_audit_refs;
DROP TABLE IF EXISTS nss_alerts;
DROP TABLE IF EXISTS nss_key_usage;
DROP TABLE IF EXISTS nss_keys;
DROP TABLE IF EXISTS nss_collisions;
DROP TABLE IF EXISTS posix_group_members;
DROP TABLE IF EXISTS posix_groups;
DROP TABLE IF EXISTS posix_users;
