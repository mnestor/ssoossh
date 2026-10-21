-- The health of the OIDC UserInfo endpoint (authentication.userinfo), one
-- row shared by every instance through the database, so failures count
-- across the fleet and a pause holds on every instance at once. See
-- server/model/userinfo_health.go and service.UserInfoHealthService.
--
-- Never a token and never a claim: last_error is the call's error text
-- with the access token scrubbed out.
CREATE TABLE oidc_userinfo_health (
    -- Always 'userinfo': one endpoint, one row.
    id                   TEXT PRIMARY KEY NOT NULL,
    -- Failed calls since the last success. A success resets it to 0.
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT NOT NULL DEFAULT '',
    last_success_at      TIMESTAMPTZ,
    last_failure_at      TIMESTAMPTZ,
    -- Set when the failure policy paused the calls (10 failures in a
    -- row): logins then skip UserInfo, except one probe per window.
    paused               BOOLEAN NOT NULL DEFAULT FALSE,
    paused_at            TIMESTAMPTZ,
    -- When the next probe may run. The instance whose conditional update
    -- moves it forward is the one that probes.
    next_probe_at        TIMESTAMPTZ
);
