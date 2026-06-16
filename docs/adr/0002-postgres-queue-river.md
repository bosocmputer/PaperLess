# ADR: Postgres-backed job queue (River), no Redis in Phase 1

## Status

Accepted (2026-06-16)

## Context

PaperLess needs background jobs: thumbnail/final-PDF generation, SML sync (confirm/lock) with retry, and notifications. The requirements demand idempotent, retryable jobs with backoff and admin visibility. Deployment is on-premise, maintained by the customer, so every additional infrastructure component is an operational cost and a failure point.

## Options

- Option A: River (Postgres-backed queue) — jobs live in the existing PaperLess Postgres.
- Option B: Redis + a worker library (asynq/sidekiq-style).
- Option C: RabbitMQ / dedicated broker.

## Decision

Option A (River). Jobs are stored in PaperLess Postgres; the worker claims and runs them with backoff + max attempts. No Redis/broker in Phase 1.

## Consequences

- Positive: one fewer component on-prem; jobs are transactional with domain state (enqueue in the same transaction as a state change → no lost/orphaned jobs); built-in retry/backoff; visible in the DB for the admin retry UI.
- Negative: Postgres carries queue load; very high throughput would eventually contend with transactional tables.
- Follow-up: keep job-enqueue behind a small interface so swapping to Redis later is localized.

## Regret Check

If document volume or notification fan-out outgrows Postgres queue throughput, we add Redis and move high-volume queues there. Early signal: queue table bloat, claim latency, or vacuum pressure attributable to job churn.
