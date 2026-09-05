-- The host context, snapshotted onto the decision.
-- See https://mnestor.github.io/ssoossh/internals/host-context/.
--
-- certificate_requests already holds all of this, and the approval page
-- reads it from there. The certificate history could not: a certificate
-- links to its request, but certificate_request_decisions is the permanent,
-- append-only record and deliberately copies rather than references, so
-- that requests can be pruned without taking the audit trail with them (see
-- model.CertificateRequestDecision). Reading the host context through the
-- request would have made the certificate view the one place that breaks
-- the day pruning lands.
--
-- So it is copied here, at decision time, the same way the approver's
-- identity, address and headers already are. The compact set, matching what
-- every cert.* audit event carries (service.hostContextDetail) -- the long
-- tail (caller pids, os, client clock, CA fingerprints) stays on the
-- request, where cert.requested records it.
--
-- reported_username and reported_hostname, not username and hostname: the
-- table already has a username, and it is the approver's. These two are the
-- requester's -- "who and where asked" -- and which pair of request columns
-- they come from depends on the type, which is what
-- model.CertificateRequest.ReportedIdentity decides and what the backfill
-- below repeats in SQL.

ALTER TABLE certificate_request_decisions ADD COLUMN reported_username TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN reported_hostname TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN pam_service TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN tty TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN remote_host TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN requesting_user TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN process TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN machine_id TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_request_decisions ADD COLUMN client TEXT NOT NULL DEFAULT '';

-- Backfill from the request, for every decision whose request is still
-- here. Without it the certificate view would show the context only for
-- decisions made after this release, and be blank for the entire history
-- that already has the data one join away. A decision whose request has
-- gone keeps the defaults, which is the same empty a pre-host-context row
-- would have given anyway.
--
-- One UPDATE ... FROM, where the SQLite file needs a correlated subquery
-- per column: same result, and the join is stated once.
--
-- The CASE is ReportedIdentity: a pam or console request carries the
-- account in username/hostname, every other type in
-- local_username/local_hostname. Picking one pair unconditionally is how a
-- user certificate ends up reporting nobody.
UPDATE certificate_request_decisions d
SET reported_username = CASE WHEN cr.type IN ('pam', 'console') THEN cr.username ELSE cr.local_username END,
    reported_hostname = CASE WHEN cr.type IN ('pam', 'console') THEN cr.hostname ELSE cr.local_hostname END,
    pam_service = cr.pam_service,
    tty = cr.tty,
    remote_host = cr.remote_host,
    requesting_user = cr.requesting_user,
    process = cr.process,
    machine_id = cr.machine_id,
    client = cr.client
FROM certificate_requests cr
WHERE cr.id = d.certificate_request_id;
