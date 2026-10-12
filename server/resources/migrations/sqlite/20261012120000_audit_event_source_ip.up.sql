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

-- Backfill from the payload. json_valid guards json_type and json_extract,
-- which raise on malformed JSON, so one corrupt row cannot fail the
-- migration; CASE evaluates lazily, which is what makes the guard work. Only
-- a string source_ip inside a detail object is copied, as written. The
-- recorder now stores the canonical form of the address (what the feed
-- filter compares against); SQL cannot canonicalize one, so an older row
-- written in another spelling (an IPv4-mapped IPv6 address from a trusted
-- proxy's header, say) keeps that spelling and is not matched by the
-- filter. The payload still holds it, and the shipped log is the archive.
UPDATE audit_events
SET source_ip = CASE
    WHEN json_valid(payload) THEN
        CASE json_type(payload, '$.detail.source_ip')
            WHEN 'text' THEN json_extract(payload, '$.detail.source_ip')
            ELSE ''
        END
    ELSE ''
END;

-- "The rows from address X, newest first": the feed filtered to one source.
-- id completes the created_at, id order the feed pages in, so the index
-- alone answers both the order and the window.
CREATE INDEX idx_audit_events_source_ip ON audit_events (source_ip, created_at, id);

-- "The rows of action X from address Y": the feed filtered both ways, which
-- is how a run of network refusals from one place is read. Two equality
-- columns, then the page order.
CREATE INDEX idx_audit_events_action_source_ip ON audit_events (action, source_ip, created_at, id);
