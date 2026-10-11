-- How much each person sees, kept apart for the two places it applies: the
-- web UI's covered pages, and the notification emails sent to them. Values
-- are 'simplified' or 'advanced' (model.DetailLevel).
--
-- Nullable with no default on purpose. NULL means "not chosen yet", and the
-- first login after this migration fills it from that session's roles
-- (advanced for SOC and admin, simplified for everyone else). Filling it
-- here instead would need group membership, which is never persisted as an
-- input (see the invariants page), and a later change of groups must not
-- move a level the person already has.
ALTER TABLE users ADD COLUMN web_detail_level TEXT;
ALTER TABLE users ADD COLUMN email_detail_level TEXT;
