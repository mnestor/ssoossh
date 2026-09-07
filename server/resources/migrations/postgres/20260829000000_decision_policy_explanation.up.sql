-- Record why an approval got the lifetime and extensions it got: the
-- winning policy tier, the condition it matched, the source rule, the
-- ceilings, and the effective values, as one structured JSON document
-- (see service.PolicyExplanation). Empty for denials and for decisions
-- predating the column. See
-- https://mnestor.github.io/ssoossh/operations/certificate-policy/.

ALTER TABLE certificate_request_decisions ADD COLUMN policy_explanation TEXT NOT NULL DEFAULT '';
