CREATE TABLE ca_signer_keys (
    fingerprint TEXT PRIMARY KEY NOT NULL,
    public_key TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX idx_ca_signer_keys_expires_at ON ca_signer_keys(expires_at);
