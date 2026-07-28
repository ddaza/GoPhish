# Architecture Decision Records (ADRs)

ADRs capture major architectural decisions for GoPhish. See `Agents.md` §8 for
when an ADR is required and what counts as a "major" change.

## Format

Each ADR is a file `NNNN-short-title.md` using `template.md`. Number
sequentially, zero-padded (e.g. `0001-...`). Keep them lightweight.

## Index

- `0001-scope-free-freemium-sources-only.md` — only free/freemium OSINT
  sources; no paid reputation services (VirusTotal, URLScan, ...).
