-- audit.events is copied to write-once storage by the worker. export_seq names the export a row went
-- out with; NULL means it has not gone out yet. A mark on the row rather than a position to continue
-- from: created_at is when a transaction began and id is random, so a slow transaction can commit a
-- row behind any position already passed, and that row would never be copied.
ALTER TABLE audit.events ADD COLUMN export_seq BIGINT;

-- Runtime roles write the columns an event is made of and nothing else: not export_seq, which would
-- keep a row out of the copy, and not created_at, which would date it where nobody looks.
REVOKE INSERT ON audit.events FROM stratum_app, stratum_worker, stratum_webhook;
GRANT INSERT (organization_id, actor, action, resource, status_code, metadata, ip, user_agent)
    ON audit.events TO stratum_app, stratum_worker, stratum_webhook;

-- An account deletion under way anonymizes the account's audit rows before the account is gone, and
-- verifying the copy allows for that. Only the API asks for a deletion, so the worker, which also
-- anonymizes, cannot pretend one is under way: it may not add a task, and of a task it may change
-- only how it went, never what it is, for whom, or when it was asked for.
REVOKE INSERT, UPDATE ON account.tasks FROM stratum_worker;
GRANT UPDATE (status, result, error, completed_at) ON account.tasks TO stratum_worker;

-- One row per exported object. object_id is part of the object's name. salt keys the digests that
-- stand in for a person's data, so that only someone who also holds the database can test a guess
-- against them.
CREATE TABLE audit.exports (
    seq          BIGINT      PRIMARY KEY CHECK (seq > 0),
    object_id    UUID        NOT NULL DEFAULT gen_random_uuid(),
    salt         TEXT        NOT NULL DEFAULT gen_random_uuid()::text,
    claimed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    sha256       TEXT,
    retain_until TIMESTAMPTZ,
    uploaded_at  TIMESTAMPTZ,
    CONSTRAINT ck_exports_uploaded_consistent
        CHECK ((uploaded_at IS NULL) = (sha256 IS NULL) AND (uploaded_at IS NULL) = (retain_until IS NULL))
);
-- Written only through the functions below; only the worker reads it.
REVOKE ALL ON audit.exports FROM stratum_app, stratum_worker, stratum_webhook;
GRANT SELECT ON audit.exports TO stratum_worker;

-- Returns the export still to be uploaded, or marks the oldest unexported rows as the next one. At
-- most 5,000 rows, whatever the caller asks for. No row is returned when there is nothing to export.
-- min_seq is one above the highest number already in the bucket: a database restored from an older
-- backup would otherwise number new objects like ones already stored. A min_seq further ahead than a
-- restore could explain is not the exporter's doing, and is ignored. Rows the retention sweep has
-- locked are left for the next claim rather than waited for.
CREATE FUNCTION audit.claim_export(batch int, min_seq bigint)
    RETURNS TABLE (seq bigint, object_id uuid, salt text, claimed_at timestamptz)
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
    current_seq bigint;
BEGIN
    -- One claim at a time: two would take the same number.
    LOCK TABLE audit.exports IN SHARE ROW EXCLUSIVE MODE;

    SELECT e.seq INTO current_seq FROM audit.exports e WHERE e.uploaded_at IS NULL ORDER BY e.seq LIMIT 1;
    IF current_seq IS NULL THEN
        SELECT COALESCE(max(e.seq), 0) + 1 INTO current_seq FROM audit.exports e;
        IF min_seq > current_seq AND min_seq <= current_seq + 100000 THEN
            current_seq := min_seq;
        END IF;
        UPDATE audit.events SET export_seq = current_seq
        WHERE id IN (
            SELECT ev.id FROM audit.events ev
            WHERE ev.export_seq IS NULL
            ORDER BY ev.created_at
            LIMIT LEAST(GREATEST(batch, 1), 5000)
            FOR UPDATE SKIP LOCKED
        );
        IF NOT FOUND THEN
            RETURN;
        END IF;
        INSERT INTO audit.exports (seq) VALUES (current_seq);
    END IF;

    RETURN QUERY
        SELECT e.seq, e.object_id, e.salt, e.claimed_at FROM audit.exports e WHERE e.seq = current_seq;
END;
$$;
REVOKE ALL ON FUNCTION audit.claim_export(int, bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION audit.claim_export(int, bigint) TO stratum_worker;

-- Records the upload of a claimed export and reports whether it is recorded, by this call or an
-- earlier one. A lock shorter than the table's own floor is refused, so a stolen worker login cannot
-- record an export as already expired.
CREATE FUNCTION audit.finish_export(p_seq bigint, p_sha256 text, p_retain_until timestamptz) RETURNS boolean
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
    IF p_retain_until < (SELECT e.claimed_at FROM audit.exports e WHERE e.seq = p_seq) + interval '365 days' THEN
        RAISE EXCEPTION 'finish_export: % is locked for less than 365 days', p_seq
            USING ERRCODE = 'check_violation';
    END IF;
    UPDATE audit.exports
    SET sha256 = p_sha256, retain_until = p_retain_until, uploaded_at = now()
    WHERE seq = p_seq AND uploaded_at IS NULL;
    RETURN EXISTS (SELECT 1 FROM audit.exports e WHERE e.seq = p_seq AND e.uploaded_at IS NOT NULL);
END;
$$;
REVOKE ALL ON FUNCTION audit.finish_export(bigint, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION audit.finish_export(bigint, text, timestamptz) TO stratum_worker;
