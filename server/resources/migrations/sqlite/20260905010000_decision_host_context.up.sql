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
-- The CASE is ReportedIdentity: a pam or console request carries the
-- account in username/hostname, every other type in
-- local_username/local_hostname. Picking one pair unconditionally is how a
-- user certificate ends up reporting nobody.
UPDATE certificate_request_decisions
SET reported_username = COALESCE((
        SELECT CASE WHEN cr.type IN ('pam', 'console') THEN cr.username ELSE cr.local_username END
        FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), ''),
    reported_hostname = COALESCE((
        SELECT CASE WHEN cr.type IN ('pam', 'console') THEN cr.hostname ELSE cr.local_hostname END
        FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), ''),
    pam_service = COALESCE((SELECT cr.pam_service FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), ''),
    tty = COALESCE((SELECT cr.tty FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), ''),
    remote_host = COALESCE((SELECT cr.remote_host FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), ''),
    requesting_user = COALESCE((SELECT cr.requesting_user FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), ''),
    process = COALESCE((SELECT cr.process FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), ''),
    machine_id = COALESCE((SELECT cr.machine_id FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), ''),
    client = COALESCE((SELECT cr.client FROM certificate_requests cr
        WHERE cr.id = certificate_request_decisions.certificate_request_id), '')
WHERE EXISTS (SELECT 1 FROM certificate_requests cr
    WHERE cr.id = certificate_request_decisions.certificate_request_id);
