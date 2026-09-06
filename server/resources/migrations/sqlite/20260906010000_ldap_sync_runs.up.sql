-- One row per directory sync pass, so a sync that ran can be told from one
-- that never fired.
--
-- Sync previously reported only to the log, which meant a UI had nowhere to
-- read the outcome from and an operator asking "is the sync even running"
-- had to go and read a log file on whichever instance happened to run it.
-- That is also what an operator-triggered sync needs in order to report
-- anything at all.
CREATE TABLE ldap_sync_runs (
    id             TEXT PRIMARY KEY NOT NULL,

    started_at     DATETIME NOT NULL,
    -- NULL while the pass is still running, which is also how a run that
    -- died with its process reads afterwards.
    finished_at    DATETIME NULL,

    -- 'schedule' or 'manual'. Named trigger_source rather than trigger
    -- because TRIGGER is a reserved word in PostgreSQL and this schema is
    -- kept identical across both dialects.
    trigger_source TEXT NOT NULL,

    -- A dry run reads the directory and reports what it would have done,
    -- writing nothing but this row. It is what makes the button safe to
    -- press during an incident.
    dry_run        INTEGER NOT NULL DEFAULT 0,

    -- The admin who pressed the button. NULL for a scheduled pass, and
    -- nulled rather than deleted with the user, since the run happened.
    actor_user_id  TEXT NULL REFERENCES users(id) ON DELETE SET NULL,

    -- The host that ran it. Jobs are not leader-elected, so every instance
    -- runs its own pass and "which log do I go and read" is a real
    -- question.
    instance       TEXT NOT NULL DEFAULT '',

    users_seen     INTEGER NOT NULL DEFAULT 0,
    found          INTEGER NOT NULL DEFAULT 0,
    missing        INTEGER NOT NULL DEFAULT 0,
    failed         INTEGER NOT NULL DEFAULT 0,
    disabled       INTEGER NOT NULL DEFAULT 0,
    reenabled      INTEGER NOT NULL DEFAULT 0,

    -- The reason the pass could not run: an unreachable directory, a failed
    -- bind. Empty for a pass that completed, whatever it concluded about
    -- individual users.
    error_message  TEXT NOT NULL DEFAULT ''
);

-- The status panel's query: the most recent run.
CREATE INDEX idx_ldap_sync_runs_started ON ldap_sync_runs (started_at DESC);
