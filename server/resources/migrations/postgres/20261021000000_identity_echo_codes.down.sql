-- Rollback: drops the bootstrap identity echo's links. A link already
-- printed stops working; nothing else is lost, since the table holds no
-- claims and every echo event stays in the audit log.
DROP TABLE IF EXISTS identity_echo_codes;
