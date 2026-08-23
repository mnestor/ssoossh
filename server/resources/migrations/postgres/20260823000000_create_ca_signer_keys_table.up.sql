CREATE TABLE ca_signer_keys (
    fingerprint TEXT PRIMARY KEY NOT NULL,
    public_key TEXT NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX idx_ca_signer_keys_expires_at ON ca_signer_keys(expires_at);
