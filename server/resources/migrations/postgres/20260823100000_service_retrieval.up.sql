-- Service retrieval: fix everything signing needs on the enrollment at
-- approval time (evaluate-at-enrollment-time contract), and log each
-- redemption for the approver and auditors.
ALTER TABLE enrollments ADD COLUMN certificate_request_id TEXT REFERENCES certificate_requests(id);
ALTER TABLE enrollments ADD COLUMN key_id TEXT NOT NULL DEFAULT '';
ALTER TABLE enrollments ADD COLUMN principals TEXT NOT NULL DEFAULT '';

CREATE TABLE enrollment_retrievals (
    id TEXT PRIMARY KEY NOT NULL,
    enrollment_id TEXT NOT NULL REFERENCES enrollments(id),
    source_ip TEXT NOT NULL DEFAULT '',
    certificate_serial BIGINT NOT NULL,
    retrieved_at TIMESTAMPTZ NOT NULL,
    succeeded BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX idx_enrollment_retrievals_enrollment_id ON enrollment_retrievals(enrollment_id);
