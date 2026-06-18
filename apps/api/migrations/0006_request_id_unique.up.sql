-- 0006_request_id_unique.up.sql
-- Phase 3 Step 1a: enforce request_id idempotency at the DB level.
--
-- The app-level SELECT-then-INSERT guard in Sign and ExternalSign is not atomic:
-- two concurrent requests with the same request_id can both pass the SELECT and
-- both write a signature_event row. This partial unique index makes the DB the
-- last line of defense: a duplicate INSERT gets SQLSTATE 23505, which the engine
-- treats as idempotent success (the winning row is already committed).
--
-- Partial index (WHERE NOT NULL AND NOT empty) so that rows with no request_id
-- (legacy/stub callers) are unaffected — they were never deduplicated by the
-- app check either, and requiring a request_id on old callers would be a
-- breaking change.
CREATE UNIQUE INDEX uq_sig_events_request
    ON signature_events (task_id, request_id)
    WHERE request_id IS NOT NULL AND request_id <> '';
