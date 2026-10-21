-- Rollback: drops the UserInfo endpoint's health row. Nothing is lost but
-- the failure count and a pause, which a later up starts afresh.
DROP TABLE IF EXISTS oidc_userinfo_health;
