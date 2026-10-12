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

-- Backfill from the payload. json_valid guards json_type and json_extract,
-- which raise on malformed JSON, so one corrupt row cannot fail the
-- migration; CASE evaluates lazily, which is what makes the guard work. Only
-- a string action is copied.
UPDATE audit_events
SET action = CASE
    WHEN json_valid(payload) THEN
        CASE json_type(payload, '$.action')
            WHEN 'text' THEN json_extract(payload, '$.action')
            ELSE ''
        END
    ELSE ''
END;

-- "The rows with action X, oldest first": the shape of a row-cap sweep that
-- trims one action before the rest, and of any page filtered to one action.
-- id completes the created_at, id order SweepAuditEvents deletes in, so the
-- index alone answers both the order and the ids.
CREATE INDEX idx_audit_events_action ON audit_events (action, created_at, id);

-- "Each user's latest row with action X": the directory sync's latest
-- auth.login per user. One seek on action, then actor groups in order with
-- their newest created_at last.
CREATE INDEX idx_audit_events_action_actor ON audit_events (action, actor_user_id, created_at);
