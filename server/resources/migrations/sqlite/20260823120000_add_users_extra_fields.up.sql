-- Operator-configured extra OIDC claim fields captured at login, as a JSON
-- map of template name -> string or array of strings ('{}' when none are
-- configured). Consumed by key ID templates. See config.OAuthFields.Extra.
ALTER TABLE users ADD COLUMN extra_fields TEXT NOT NULL DEFAULT '';
