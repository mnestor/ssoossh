-- Keyset indexes for the two admin lists that page across every row of their
-- table, newest first: the cross-user certificate history, ordered
-- (issued_at DESC, id DESC), and the service-code list, ordered
-- (created_at DESC, id DESC). Each list resumes after its cursor row with
-- "sort key < cursor's, or equal with a smaller id", and these indexes answer
-- both that bound and the order, so a page reads its rows from the index
-- rather than sorting the table.
--
-- The existing certificate index leads with user_id, which serves the
-- caller's own history but not a list with no owner filter, and enrollments
-- had no index on created_at at all. The other three lists need none: the
-- audit feed and timeline already have (created_at), (action, created_at, id),
-- (source_ip, created_at, id) and (actor_user_id|target_user_id, created_at),
-- and the user directory orders by expressions over a table the size of the
-- organization.
CREATE INDEX idx_certificates_issued_at_id ON certificates (issued_at, id);
CREATE INDEX idx_enrollments_created_at_id ON enrollments (created_at, id);
