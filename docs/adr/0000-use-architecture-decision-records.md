# 0000. Use Architecture Decision Records

- **Status:** Accepted
- **Date:** 2026-06-22

## Context

Architectural rationale for OpsDrop currently lives in two places: prose in
`CLAUDE.md` ("don't raise `SetMaxOpenConns(1)`", "the capabilities probe is
intentional") and commit messages that reference decisions by hash (`1e9097c`,
`b988df6`). Both are useful but incomplete for capturing *why* a non-obvious
choice was made:

- `CLAUDE.md` describes current state and what not to break. It deliberately does
  not preserve rejected alternatives, so the reasoning behind a choice is lost
  once the prose is updated.
- Commit messages record *what* changed and are hard to browse as a body of
  decisions.

As the project takes on less obvious choices (e.g. how to expose MCP tooling),
the absence of a durable "why" record means decisions risk being re-litigated or,
worse, undone because they looked like oversights.

## Decision

We will keep Architecture Decision Records in `docs/adr/`, one markdown file per
decision, following Michael Nygard's format (see `template.md`). We will write an
ADR only for non-obvious decisions, and keep the practice deliberately
lightweight: no tooling, no CI enforcement, immutable records, supersede rather
than edit.

## Consequences

- Non-obvious decisions gain a durable, browsable home that survives `CLAUDE.md`
  edits and is easier to find than git archaeology.
- A small ongoing cost: contributors must recognise when a change warrants an ADR
  and write one. Routine changes do not, so the overhead stays low.
- `CLAUDE.md` and commit messages keep their roles; ADRs add the "why",
  cross-referencing the others where useful.

## Alternatives considered

- **Keep rationale only in `CLAUDE.md`.** Rejected: it tracks current state and
  drops rejected alternatives, so it cannot serve as decision history.
- **Rely on commit messages and PR descriptions.** Rejected: not browsable as a
  set, and easily buried.
- **A heavier process (RFCs, a wiki, enforced templates).** Rejected as
  disproportionate for a project this size; the value is in recording the *why*,
  not in ceremony.
