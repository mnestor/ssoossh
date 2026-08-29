-- Downgrade: drop the recorded policy explanations. The decision rows
-- themselves are untouched; only the "why this lifetime" document is lost.

ALTER TABLE certificate_request_decisions DROP COLUMN policy_explanation;
