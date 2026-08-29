-- Bind the approval page to the first browser that opens it. On the first
-- document GET of /approve/<id> the server mints a claim token, sets it as
-- a cookie scoped to that path, and stores its hash here; every later GET
-- must present the matching cookie or is turned away. See
-- service.CertRequestService.ClaimApprovalPage,
-- middleware.ApprovalClaimMiddleware, and
-- docs/proposals/gui-client-approval-flow.md section 6.

-- Hex SHA-256 of the claim cookie's value, never the value itself, so a
-- database read does not yield a cookie that unlocks someone's pending
-- approval page. NULL means unclaimed.
ALTER TABLE certificate_requests ADD COLUMN claim_token_hash TEXT;

-- When the claim happened. Feeds the cookie-blocked heuristic: a claimed
-- request revisited cookieless by the same user agent shortly after
-- claiming is a browser refusing cookies, not a second client.
ALTER TABLE certificate_requests ADD COLUMN claimed_at DATETIME;

-- User agent that claimed the page, kept for the same heuristic and for
-- mismatch logging (a second client hitting a claimed page is a
-- high-signal phishing indicator).
ALTER TABLE certificate_requests ADD COLUMN claim_user_agent TEXT NOT NULL DEFAULT '';
