-- The administrative policy the requesting machine claimed was in force on
-- it, as a JSON-encoded apitypes.ClientPolicy. Empty when none was claimed,
-- which is every request from a machine without MDM/Group Policy/an enforce
-- file, and every request from a client predating the field.
--
-- Display context, not an input to issuance: it lets an approver tell a rule
-- imposed on the requester from a choice the requester made. Nullable with no
-- default so an existing row reads as "said nothing", which is what it did.
ALTER TABLE certificate_requests ADD COLUMN client_policy TEXT;
