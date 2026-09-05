-- Downgrade: drop the snapshotted host context. Nothing is lost that the
-- previous release could show, and certificate_requests still holds the
-- same values for every request that has not been pruned.

ALTER TABLE certificate_request_decisions DROP COLUMN client;
ALTER TABLE certificate_request_decisions DROP COLUMN machine_id;
ALTER TABLE certificate_request_decisions DROP COLUMN process;
ALTER TABLE certificate_request_decisions DROP COLUMN requesting_user;
ALTER TABLE certificate_request_decisions DROP COLUMN remote_host;
ALTER TABLE certificate_request_decisions DROP COLUMN tty;
ALTER TABLE certificate_request_decisions DROP COLUMN pam_service;
ALTER TABLE certificate_request_decisions DROP COLUMN reported_hostname;
ALTER TABLE certificate_request_decisions DROP COLUMN reported_username;
