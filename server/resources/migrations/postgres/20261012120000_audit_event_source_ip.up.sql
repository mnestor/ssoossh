-- The source address of each audit event as its own indexed column, so the
-- admin feed can filter to "everything from this address" from an index
-- instead of matching the JSON payload. A grouping key like action,
-- actor_user_id and target_user_id, never the record: the payload is
-- authoritative, and the recorder writes both from the same event
-- (service.AuditService.prepare).
--
-- Which address: the payload's detail.source_ip, the address the server
-- observed for the client the event is about (the certificate request's
-- client on every cert.* event, the retrieving client on
-- enrollment.redeemed, the refused caller on cert.request_refused_network,
-- the caller on user.disable_refused). Never approver_ip, which is a
-- different party's connection, and never remote_host, which the client
-- reported. Events with no observed source hold ''.
--
-- NOT NULL DEFAULT '' rather than nullable, like action: '' matches no
-- address filter, which is the side that claims nothing.
ALTER TABLE audit_events ADD COLUMN source_ip TEXT NOT NULL DEFAULT '';

-- Backfill from the payload, the same way 20261012000000_audit_event_action
-- backfills action: a plain payload::json cast raises on the first malformed
-- row, and one corrupt row must not fail the migration, so the read runs in
-- a function that turns any failure into ''. Only a string source_ip inside
-- a detail object is copied, as written (see the SQLite copy).
--
-- \u0000 escapes are rewritten to \u0001 first, because Postgres' JSON
-- functions refuse them anywhere in the document and Go writes one for a
-- NUL in any detail string. Safe on valid JSON: a backslash only occurs
-- inside a string, so only string contents move. An address never holds a
-- NUL, so the value copied is never one the rewrite touched.
--
-- The function lives in the schema the migration writes to and is dropped
-- below, so it needs no privilege the migration does not already use.
CREATE FUNCTION ssoossh_audit_payload_source_ip(payload TEXT) RETURNS TEXT
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    doc JSON;
BEGIN
    doc := replace(payload, E'\\u0000', E'\\u0001')::json;
    IF json_typeof(doc -> 'detail' -> 'source_ip') = 'string' THEN
        RETURN doc -> 'detail' ->> 'source_ip';
    END IF;
    RETURN '';
EXCEPTION WHEN OTHERS THEN
    RETURN '';
END
$$;

UPDATE audit_events SET source_ip = ssoossh_audit_payload_source_ip(payload);

DROP FUNCTION ssoossh_audit_payload_source_ip(TEXT);

-- "The rows from address X, newest first": the feed filtered to one source.
-- id completes the created_at, id order the feed pages in, so the index
-- alone answers both the order and the window.
CREATE INDEX idx_audit_events_source_ip ON audit_events (source_ip, created_at, id);

-- "The rows of action X from address Y": the feed filtered both ways, which
-- is how a run of network refusals from one place is read. Two equality
-- columns, then the page order.
CREATE INDEX idx_audit_events_action_source_ip ON audit_events (action, source_ip, created_at, id);
