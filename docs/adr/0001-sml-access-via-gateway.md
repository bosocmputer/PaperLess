# ADR: SML access only through sml-api-bybos

## Status

Accepted (2026-06-16)

## Context

PaperLess must read documents/metadata from SML and (later) write Confirm/Lock back. SML is a production PostgreSQL ERP on the customer LAN. There is already a Go/Gin/pgx gateway, `sml-api-bybos`, that owns SML connections (multi-tenant pools, API-key auth, logging). PaperLess is a new system that also needs SML.

## Options

- Option A: PaperLess connects to the SML database directly with its own pgx pool.
- Option B: PaperLess talks to SML only through `sml-api-bybos`, which we extend with the few endpoints we need.
- Option C: PaperLess keeps a synced copy of SML data locally.

## Decision

Option B. PaperLess never opens an SML DB connection. We add `paperless/documents/:doc_no` (read), `.../confirm`, and `.../lock` endpoints to `sml-api-bybos`, reusing its conventions (envelope, zap logs, request-id, erp_logs).

## Consequences

- Positive: one place owns SML schema knowledge and connection pooling; smaller blast radius on the ERP; consistent auth/logging; matches blueprint §29 boundary guidance.
- Negative: a network hop and a cross-repo dependency; Confirm/Lock work is blocked until SML team confirms fields (tracked in `docs/sml-integration-notes.md`).
- Follow-up: PaperLess code depends on a `SmlDocumentGateway` interface with a mock impl so Phase 1/2 proceed without the real endpoints.

## Regret Check

If `sml-api-bybos` becomes a bottleneck or its release cadence blocks PaperLess, we may want a dedicated read path. Early signal: sync latency or contention on the gateway, or frequent coupled deploys. The gateway interface keeps that swap localized.
