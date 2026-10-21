-- The bootstrap identity echo's one-time links, minted on the server host
-- by `ssoosshd identity echo` and redeemed once by the browser that opens
-- one. See docs/proposals/identity-bootstrap-echo.md and
-- server/model/identity_echo_code.go.
--
-- Only the code's SHA-256 is stored, never the code, and never a claim: the
-- echoed claims reach the page in the redirect fragment. Minting a code
-- deletes every other row, so the table holds at most one live code.
CREATE TABLE identity_echo_codes (
    id           TEXT PRIMARY KEY NOT NULL,
    -- Hex SHA-256 of the code. Unique, and the column a redeem looks up.
    code_hash    TEXT NOT NULL UNIQUE,
    -- The OS account and host the command ran as. Context, not proof.
    requested_by TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    -- Set once, by the conditional update that consumes the code.
    used_at      TIMESTAMPTZ,
    -- The address the server observed for the redeeming browser.
    used_from    TEXT NOT NULL DEFAULT '',
    -- When the sign-in came back, and the error it ended in ('' for
    -- verified claims). Never a claim value.
    completed_at TIMESTAMPTZ,
    outcome      TEXT NOT NULL DEFAULT ''
);
