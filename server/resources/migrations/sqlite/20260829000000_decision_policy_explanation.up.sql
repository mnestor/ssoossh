-- Record why an approval got the lifetime and extensions it got: the
-- winning policy tier, the condition it matched, the source rule, the
-- ceilings, and the effective values, as one structured JSON document
-- (see service.PolicyExplanation). Empty for denials and for decisions
-- predating the column. See docs/proposals/claim-driven-certificate-policy.md
-- finding F4.

ALTER TABLE certificate_request_decisions ADD COLUMN policy_explanation TEXT NOT NULL DEFAULT '';
