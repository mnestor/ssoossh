-- Downgrade: drop the host-context and decision-content columns. The
-- previous release never read them, so nothing is lost that it could show.

ALTER TABLE certificate_request_decisions DROP COLUMN granted_options;
ALTER TABLE certificate_request_decisions DROP COLUMN principals;

ALTER TABLE certificate_requests DROP COLUMN trusted_ca_fingerprints;
ALTER TABLE certificate_requests DROP COLUMN client_time;
ALTER TABLE certificate_requests DROP COLUMN client_mode;
ALTER TABLE certificate_requests DROP COLUMN client;
ALTER TABLE certificate_requests DROP COLUMN os;
ALTER TABLE certificate_requests DROP COLUMN machine_id;
ALTER TABLE certificate_requests DROP COLUMN caller_ppid;
ALTER TABLE certificate_requests DROP COLUMN caller_pid;
ALTER TABLE certificate_requests DROP COLUMN caller_uid;
ALTER TABLE certificate_requests DROP COLUMN process;
ALTER TABLE certificate_requests DROP COLUMN requesting_user;
