-- Console login: a fourth certificate type, plus the console context an
-- approver needs to recognise the login they are being asked to authorize.
-- See docs/proposals/console-login-pam.md.
--
-- Postgres can alter a CHECK constraint in place, so this is the whole of
-- it here; the SQLite side of the same migration has to rebuild both
-- tables, which is why the two files look nothing alike.

ALTER TABLE certificate_requests DROP CONSTRAINT chk_certificate_requests_type;
ALTER TABLE certificate_requests ADD CONSTRAINT chk_certificate_requests_type
    CHECK (type IN ('user', 'service', 'pam', 'console'));

ALTER TABLE certificates DROP CONSTRAINT chk_certificates_type;
ALTER TABLE certificates ADD CONSTRAINT chk_certificates_type
    CHECK (type IN ('user', 'service', 'pam', 'console'));

-- The short code a console displays for a human to type into the web UI,
-- normalized (Crockford Base32, no separators). Console requests only;
-- empty for every other type. It is a lookup key for an
-- already-authenticated approver, not a capability — resolving one needs a
-- session.
ALTER TABLE certificate_requests ADD COLUMN user_code TEXT NOT NULL DEFAULT '';

-- Console context, all of it self-reported by an unauthenticated caller and
-- rendered as such: which machine, which PAM service, which terminal, and
-- whether PAM_RHOST says this is not a console at all. Bounded on the way
-- in so a caller cannot write arbitrary volume here.
ALTER TABLE certificate_requests ADD COLUMN hostname TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_requests ADD COLUMN pam_service TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_requests ADD COLUMN tty TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_requests ADD COLUMN remote_host TEXT NOT NULL DEFAULT '';

-- Uniqueness over live rows only. Two pending requests sharing a code would
-- let one approver's typed code resolve to a stranger's request; a resolved
-- one is no longer reachable by code, so retiring it from the index keeps
-- the 40-bit space from filling up over the life of a deployment.
CREATE UNIQUE INDEX idx_certificate_requests_user_code
    ON certificate_requests(user_code)
    WHERE user_code <> '' AND status IN ('pending', 'signing');
