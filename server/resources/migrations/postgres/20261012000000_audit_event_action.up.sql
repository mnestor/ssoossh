-- The action of each audit event as its own indexed column, so a filter on
-- action ("every auth.login", "the oldest rows of one action") reads an
-- index instead of matching the JSON payload. Like actor_user_id and
-- target_user_id it is a grouping key, never the record: the payload is
-- authoritative, and the recorder writes both from the same event so they
-- cannot disagree (service.AuditService.prepare).
--
-- NOT NULL DEFAULT '' rather than nullable: every event has an action, and a
-- row whose payload cannot be read gets '' here, which matches no action
-- filter. That is the side that claims nothing.
ALTER TABLE audit_events ADD COLUMN action TEXT NOT NULL DEFAULT '';

-- Backfill from the payload. A plain payload::json cast raises on the first
-- malformed row, and one corrupt row must not fail the migration, so the
-- read runs in a function that turns any failure into ''. Only a string
-- action is copied.
--
-- Postgres' JSON functions refuse a \u0000 escape anywhere in the document
-- (it cannot become text), and Go writes one for a NUL in any detail
-- string, so each is rewritten to \u0001 first. That is safe on valid JSON:
-- a backslash only occurs inside a string, so the swap either replaces a
-- real escape with another valid one or, after an escaped backslash,
-- changes literal text. Either way only string contents move, never an
-- action the recorder wrote.
--
-- The function lives in the schema the migration writes to and is dropped
-- below, so it needs no privilege the migration does not already use.
CREATE FUNCTION ssoossh_audit_payload_action(payload TEXT) RETURNS TEXT
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    doc JSON;
BEGIN
    doc := replace(payload, E'\\u0000', E'\\u0001')::json;
    IF json_typeof(doc -> 'action') = 'string' THEN
        RETURN doc ->> 'action';
    END IF;
    RETURN '';
EXCEPTION WHEN OTHERS THEN
    RETURN '';
END
$$;

UPDATE audit_events SET action = ssoossh_audit_payload_action(payload);

DROP FUNCTION ssoossh_audit_payload_action(TEXT);

-- "The rows with action X, oldest first": the shape of a row-cap sweep that
-- trims one action before the rest, and of any page filtered to one action.
-- id completes the created_at, id order SweepAuditEvents deletes in, so the
-- index alone answers both the order and the ids.
CREATE INDEX idx_audit_events_action ON audit_events (action, created_at, id);

-- "Each user's latest row with action X": the directory sync's latest
-- auth.login per user. One seek on action, then actor groups in order with
-- their newest created_at last.
CREATE INDEX idx_audit_events_action_actor ON audit_events (action, actor_user_id, created_at);
