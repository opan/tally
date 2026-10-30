-- Tally schema. Applied automatically at startup (idempotent).

CREATE TABLE IF NOT EXISTS users (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    username      text        NOT NULL UNIQUE,
    password_hash text,                          -- NULL for accounts that log in through aio
    aio_user_id   text        UNIQUE,            -- set when linked to an all-in-one account
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id         text        PRIMARY KEY,          -- random session token
    user_id    uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id_token   text,                             -- aio ID token (logout hint); NULL for local logins
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id);

-- A tally is one leaderboard a group runs, e.g. "Bala Bulu Point Rank".
CREATE TABLE IF NOT EXISTS tallies (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    uuid        NOT NULL REFERENCES users(id),
    slug        text        NOT NULL UNIQUE,
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    template    text        NOT NULL DEFAULT 'badminton_doubles',
    visibility  text        NOT NULL DEFAULT 'link' CHECK (visibility IN ('public', 'link', 'private')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tally_admins (
    tally_id uuid        NOT NULL REFERENCES tallies(id) ON DELETE CASCADE,
    user_id  uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role     text        NOT NULL DEFAULT 'admin' CHECK (role IN ('owner', 'admin')),
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tally_id, user_id)
);
CREATE INDEX IF NOT EXISTS tally_admins_user_idx ON tally_admins (user_id);

-- Players are names an admin types in; user_id links one to an account later.
CREATE TABLE IF NOT EXISTS participants (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tally_id     uuid        NOT NULL REFERENCES tallies(id) ON DELETE CASCADE,
    display_name text        NOT NULL,
    active       boolean     NOT NULL DEFAULT true,
    user_id      uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS participants_name_uniq ON participants (tally_id, lower(display_name));

-- A period is a season with its own points rule (see ScoringConfig).
CREATE TABLE IF NOT EXISTS periods (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tally_id   uuid        NOT NULL REFERENCES tallies(id) ON DELETE CASCADE,
    name       text        NOT NULL,
    starts_on  date        NOT NULL,
    ends_on    date        NOT NULL,
    scoring    jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_on >= starts_on)
);
CREATE INDEX IF NOT EXISTS periods_tally_idx ON periods (tally_id, starts_on DESC);

-- A round is one play day ("Week #2 - 11 Oct"); its number is its position by
-- date within the period, computed on read. Rank movement arrows compare
-- standings after consecutive rounds.
CREATE TABLE IF NOT EXISTS rounds (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    period_id  uuid        NOT NULL REFERENCES periods(id) ON DELETE CASCADE,
    played_on  date        NOT NULL,
    courts     smallint    NOT NULL DEFAULT 1 CHECK (courts BETWEEN 1 AND 3),
    created_at timestamptz NOT NULL DEFAULT now()
);
-- Upgrades from the P0/P1 shape of rounds.
ALTER TABLE rounds DROP COLUMN IF EXISTS number;
ALTER TABLE rounds DROP COLUMN IF EXISTS label;
ALTER TABLE rounds ADD COLUMN IF NOT EXISTS courts smallint NOT NULL DEFAULT 1 CHECK (courts BETWEEN 1 AND 3);
ALTER TABLE rounds ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now();
CREATE UNIQUE INDEX IF NOT EXISTS rounds_period_date_uniq ON rounds (period_id, played_on);

-- Who showed up on a play day; the shuffle picks from these.
CREATE TABLE IF NOT EXISTS round_attendance (
    round_id       uuid NOT NULL REFERENCES rounds(id) ON DELETE CASCADE,
    participant_id uuid NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
    PRIMARY KEY (round_id, participant_id)
);
-- Upgrade: attendance used to block deleting a player who never played.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint
               WHERE conname = 'round_attendance_participant_id_fkey' AND confdeltype <> 'c') THEN
        ALTER TABLE round_attendance DROP CONSTRAINT round_attendance_participant_id_fkey;
        ALTER TABLE round_attendance ADD CONSTRAINT round_attendance_participant_id_fkey
            FOREIGN KEY (participant_id) REFERENCES participants(id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS matches (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    period_id  uuid        NOT NULL REFERENCES periods(id) ON DELETE CASCADE,
    round_id   uuid        NOT NULL REFERENCES rounds(id) ON DELETE CASCADE,
    played_on  date        NOT NULL,
    notes      text        NOT NULL DEFAULT '',
    created_by uuid        REFERENCES users(id) ON DELETE SET NULL,
    updated_by uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- scheduled = on court (players picked, no score yet); done = scored, points awarded.
ALTER TABLE matches ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'done' CHECK (status IN ('scheduled', 'done'));
CREATE INDEX IF NOT EXISTS matches_round_idx ON matches (round_id, created_at);

CREATE TABLE IF NOT EXISTS match_sides (
    match_id uuid     NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    side     smallint NOT NULL CHECK (side IN (1, 2)),
    games    int[]    NOT NULL,                  -- one entry per game; continuous format has one
    total    int      NOT NULL,
    result   text     NOT NULL CHECK (result IN ('win', 'draw', 'loss')),
    points   int      NOT NULL DEFAULT 0,         -- what each player on this side earned
    PRIMARY KEY (match_id, side)
);
ALTER TABLE match_sides ADD COLUMN IF NOT EXISTS points int NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS match_players (
    match_id       uuid     NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    side           smallint NOT NULL CHECK (side IN (1, 2)),
    participant_id uuid     NOT NULL REFERENCES participants(id),
    PRIMARY KEY (match_id, participant_id)
);
CREATE INDEX IF NOT EXISTS match_players_participant_idx ON match_players (participant_id);

-- Points ledger. Leaderboard = SUM(points) per participant per period.
-- match_id is NULL for manual adjustments.
CREATE TABLE IF NOT EXISTS point_awards (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    period_id      uuid        NOT NULL REFERENCES periods(id) ON DELETE CASCADE,
    participant_id uuid        NOT NULL REFERENCES participants(id),
    match_id       uuid        REFERENCES matches(id) ON DELETE CASCADE,
    points         int         NOT NULL,
    reason         text        NOT NULL DEFAULT '',
    created_by     uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS point_awards_period_idx ON point_awards (period_id, participant_id);
CREATE INDEX IF NOT EXISTS point_awards_match_idx ON point_awards (match_id);

-- Who changed what. before/after are JSON snapshots of the entity.
CREATE TABLE IF NOT EXISTS audit_log (
    id        bigserial   PRIMARY KEY,
    tally_id  uuid        NOT NULL REFERENCES tallies(id) ON DELETE CASCADE,
    actor_id  uuid        REFERENCES users(id) ON DELETE SET NULL,
    action    text        NOT NULL,              -- create | update | delete
    entity    text        NOT NULL,              -- match | participant | period | award ...
    entity_id text        NOT NULL,
    before    jsonb,
    after     jsonb,
    at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_log_tally_idx ON audit_log (tally_id, at DESC);

-- Poster branding: optional texts on the tally, optional logo image.
ALTER TABLE tallies ADD COLUMN IF NOT EXISTS branding jsonb NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS tally_logos (
    tally_id     uuid        PRIMARY KEY REFERENCES tallies(id) ON DELETE CASCADE,
    content_type text        NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/webp')),
    data         bytea       NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Team presets choose players per side at creation; NULL = the preset's size.
ALTER TABLE tallies ADD COLUMN IF NOT EXISTS side_size smallint CHECK (side_size BETWEEN 1 AND 11);

-- Every award belongs to a play day, so manual points (match_id NULL) show up
-- in "as of" standings too. Backfill awards that came from matches.
ALTER TABLE point_awards ADD COLUMN IF NOT EXISTS round_id uuid REFERENCES rounds(id) ON DELETE CASCADE;
UPDATE point_awards pa SET round_id = m.round_id FROM matches m WHERE pa.match_id = m.id AND pa.round_id IS NULL;
CREATE INDEX IF NOT EXISTS point_awards_round_idx ON point_awards (round_id);

-- Site admin pages (RFC-002 in all-in-one): aio decides who is an admin on
-- every admin request. is_admin_hint is the status at login, used only to show
-- the link. admin_views records every admin page view (who looked at whose data).
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS is_admin_hint boolean NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS admin_views (
    id              bigserial   PRIMARY KEY,
    admin_id        uuid        NOT NULL REFERENCES users(id),
    page            text        NOT NULL,
    subject_user_id uuid        REFERENCES users(id),  -- NULL for the overview
    viewed_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS admin_views_admin_idx ON admin_views (admin_id, viewed_at DESC);
-- Read-only tally pages for site admins: which tally, and the season, player
-- or play day id within it.
ALTER TABLE admin_views ADD COLUMN IF NOT EXISTS tally_id uuid REFERENCES tallies(id) ON DELETE SET NULL;
ALTER TABLE admin_views ADD COLUMN IF NOT EXISTS detail text NOT NULL DEFAULT '';

-- RFC-001: points entered per player without match details ('score', counts
-- as one match played), next to points from matches ('match') and bonuses or
-- penalties ('extra'). Backfill: rows with a match_id came from matches.
ALTER TABLE point_awards ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'extra' CHECK (kind IN ('match', 'score', 'extra'));
UPDATE point_awards SET kind = 'match' WHERE match_id IS NOT NULL AND kind <> 'match';
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'point_awards_kind_match') THEN
        ALTER TABLE point_awards ADD CONSTRAINT point_awards_kind_match CHECK ((kind = 'match') = (match_id IS NOT NULL));
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS point_awards_round_player_idx ON point_awards (round_id, participant_id);
