# Architecture Decision Records

This directory holds **Architecture Decision Records (ADRs)** — short documents
that capture a single non-obvious decision: its context, the choice made, and
the consequences. They preserve the *why* (including rejected alternatives) at
the moment the decision was made.

ADRs complement, rather than duplicate, the other docs:

- **`CLAUDE.md`** is current-state guidance — how the system works today and what
  not to break. It does not preserve rejected alternatives.
- **Git commit messages** record the change. ADRs record the reasoning behind it.

## When to write one

Write an ADR only when a decision is **non-obvious** — something a future reader
(or you, in six months) would otherwise have to ask "why?" about. Examples:
surprising trade-offs, a choice between viable libraries, or a deliberate
omission that looks like an oversight. Routine changes do not need one.

## Conventions

- One decision per file: `NNNN-short-slug.md`, zero-padded, monotonically
  increasing. Numbers are **never reused or renumbered**.
- Start from [`template.md`](template.md) (Michael Nygard's format).
- ADRs are immutable once `Accepted`. To change a decision, write a **new** ADR
  and mark the old one `Superseded by ADR-NNNN`; do not edit or delete it.
- No tooling or CI gate — this is a lightweight practice, kept by hand.

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [0000](0000-use-architecture-decision-records.md) | Use Architecture Decision Records | Accepted |
| [0001](0001-mcp-transport-and-placement.md) | MCP tooling transport and placement | Accepted |
