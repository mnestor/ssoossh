-- Host context: the rest of what a PAM or console module can say about the
-- process and machine asking, and the content of an approval decision.
-- See https://mnestor.github.io/ssoossh/internals/host-context/.
--
-- Every certificate_requests column here is self-reported by an
-- unauthenticated caller, bounded on the way in, and rendered as a claim.
-- They exist so an approver of a sudo can see which command is asking on
-- which machine, and so the audit line joins against the host's own logs.

-- PAM_RUSER: who invoked the service, as opposed to username, the account
-- being authenticated. Under su or sudo's targetpw the two differ.
ALTER TABLE certificate_requests ADD COLUMN requesting_user TEXT NOT NULL DEFAULT '';
-- The PAM host process's command line, e.g. "sudo -i".
ALTER TABLE certificate_requests ADD COLUMN process TEXT NOT NULL DEFAULT '';
-- Process identity on the host. NULL means not reported; 0 is a value.
ALTER TABLE certificate_requests ADD COLUMN caller_uid BIGINT;
ALTER TABLE certificate_requests ADD COLUMN caller_pid BIGINT;
ALTER TABLE certificate_requests ADD COLUMN caller_ppid BIGINT;
-- Stable per-install identifier, so a host survives a rename in the trail.
ALTER TABLE certificate_requests ADD COLUMN machine_id TEXT NOT NULL DEFAULT '';
-- os-release PRETTY_NAME plus uname -s -r.
ALTER TABLE certificate_requests ADD COLUMN os TEXT NOT NULL DEFAULT '';
-- Module name and version, and its configured mode argument.
ALTER TABLE certificate_requests ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_requests ADD COLUMN client_mode TEXT NOT NULL DEFAULT '';
-- The host's own clock when it built the request; skew is visible here.
ALTER TABLE certificate_requests ADD COLUMN client_time TIMESTAMPTZ;
-- JSON []string of SHA256 fingerprints of the keys in the module's
-- trusted-ca-file, so the server can warn before the host rejects.
ALTER TABLE certificate_requests ADD COLUMN trusted_ca_fingerprints TEXT NOT NULL DEFAULT '';

-- What an approval granted: the selected principals and the narrowed
-- options, JSON-encoded. The signing job carried both but nothing
-- persisted them, so a failed signing left the decision's content in the
-- log alone. Empty for denials and for rows predating the columns.
ALTER TABLE certificate_request_decisions ADD COLUMN principals TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN granted_options TEXT NOT NULL DEFAULT '';
