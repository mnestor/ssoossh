-- Downgrade: drop the claim columns. Requests claimed while they existed
-- simply become unclaimed again; the next opener re-claims. Nothing else
-- reads these columns.

ALTER TABLE certificate_requests DROP COLUMN claim_token_hash;
ALTER TABLE certificate_requests DROP COLUMN claimed_at;
ALTER TABLE certificate_requests DROP COLUMN claim_user_agent;
