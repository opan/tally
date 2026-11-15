-- Demo data for trying tally locally: a made-up club, Badminton Sampai
-- Pingsan, with four Sunday play days up to today, points per player, and
-- Iuran (RFC-004): three play days fully paid, today's half paid.
--
--   ./scripts/demo.sh            (or, with tally already running on the database:)
--   docker compose exec -T db psql -U tally -d tally -v ON_ERROR_STOP=1 < scripts/demo-seed.sql
--
-- Log in as demo / demo1234. Running it again starts the club over; other
-- tallies are left alone. The schema must be applied first (start tally once).
-- Names are made up. Keep it working with the schema: TestDemoSeed runs it.
CREATE EXTENSION IF NOT EXISTS pgcrypto; -- crypt(): a bcrypt hash of the demo password

DO $$
DECLARE
    v_owner uuid;
    v_tally uuid;
    v_board uuid;
    v_season uuid;
    v_day uuid;
    v_pid uuid;
    names text[] := ARRAY['Jacque Oui', 'Prabski', 'Bahlul', 'Mas Gembul', 'Tante Smash', 'Om Netting',
        'Si Lob', 'Koh Backhand', 'Pak Rally', 'Bu Drive', 'Mbak Dropshot', 'Kang Servis',
        'Bang Jumping', 'Neng Rally', 'Mang Lob', 'Cak Deuce'];
    d int;
    i int;
    n int;
    plays int;
    played date;
    method text;
    v_name text;
BEGIN
    INSERT INTO users (username, password_hash)
    VALUES ('demo', crypt('demo1234', gen_salt('bf', 10)))
    ON CONFLICT (username) DO UPDATE SET password_hash = EXCLUDED.password_hash
    RETURNING id INTO v_owner;

    -- Start over. Payments and points go with the season; then the players.
    DELETE FROM periods WHERE periods.tally_id IN (SELECT id FROM tallies WHERE slug = 'sampai-pingsan');
    DELETE FROM tallies WHERE slug = 'sampai-pingsan';

    INSERT INTO tallies (owner_id, slug, name, description, visibility)
    VALUES (v_owner, 'sampai-pingsan', 'Badminton Sampai Pingsan', 'Mabar tiap Minggu malam. Data contoh.', 'link')
    RETURNING id INTO v_tally;
    INSERT INTO tally_admins (tally_id, user_id, role) VALUES (v_tally, v_owner, 'owner');
    INSERT INTO leaderboards (tally_id, slug, template) VALUES (v_tally, 'badminton', 'badminton_doubles')
    RETURNING id INTO v_board;
    INSERT INTO periods (tally_id, leaderboard_id, name, starts_on, ends_on, scoring)
    VALUES (v_tally, v_board, 'Musim Demo', current_date - 35, current_date + 56,
            '{"format": "continuous", "target": 42, "win_bonus": 0}')
    RETURNING id INTO v_season;
    FOREACH v_name IN ARRAY names LOOP
        INSERT INTO participants (tally_id, display_name) VALUES (v_tally, v_name);
    END LOOP;

    -- Four Sundays (or whatever day today is), the last one today.
    FOR d IN 0..3 LOOP
        played := current_date - (3 - d) * 7;
        INSERT INTO rounds (period_id, played_on, courts, fee) VALUES (v_season, played, 3, 50000)
        RETURNING id INTO v_day;
        i := 0;
        FOR v_pid, v_name IN
            SELECT p.id, p.display_name FROM participants p WHERE p.tally_id = v_tally ORDER BY p.display_name
        LOOP
            i := i + 1;
            -- Not everyone comes every week.
            CONTINUE WHEN (i + d) % 5 = 0;
            INSERT INTO round_attendance (round_id, participant_id) VALUES (v_day, v_pid);
            plays := 3 + abs(hashtext(v_name || d)) % 4;
            FOR n IN 1..plays LOOP
                INSERT INTO point_awards (period_id, round_id, participant_id, points, kind, created_by, created_at)
                VALUES (v_season, v_day, v_pid, 18 + abs(hashtext(v_name || d || n)) % 25, 'score', v_owner,
                        CASE WHEN d = 3 THEN now() - (10 - n) * interval '20 minutes'
                             ELSE (played + time '19:00' + n * interval '20 minutes') AT TIME ZONE 'Asia/Jakarta' END);
            END LOOP;
            -- Earlier play days are fully paid; today's, about half so far, in the last hour.
            CONTINUE WHEN d = 3 AND i % 2 = 0;
            method := CASE WHEN i = 1 THEN 'free' WHEN (i + d) % 3 = 0 THEN 'transfer' ELSE 'cash' END;
            INSERT INTO payments (round_id, participant_id, method, note, recorded_by, created_at)
            VALUES (v_day, v_pid, method,
                    CASE WHEN i = 1 THEN 'pelatih' WHEN method = 'transfer' AND i % 4 = 0 THEN 'BCA' ELSE '' END,
                    v_owner, CASE WHEN d = 3 THEN now() - (20 - i) * interval '3 minutes'
                                  ELSE (played + time '18:45' + i * interval '3 minutes') AT TIME ZONE 'Asia/Jakarta' END);
        END LOOP;
    END LOOP;
END $$;
