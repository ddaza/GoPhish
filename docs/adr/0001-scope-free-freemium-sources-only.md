# ADR-0001: Free / freemium sources only

- Status: Accepted
- Date: 2025-07-15

## Context

GoPhish needs OSINT signals (registration data, certificates, known-phish
feeds) to detect look-alikes. Paid, key-gated reputation services (e.g.
VirusTotal, URLScan) offer richer signals but add cost, secrets, and scope
creep. Per the YAGNI / minimal-scope note (Agents.md §1), we build only what
answers the three core questions.

## Decision

Rely exclusively on **free or freemium public data sources** (RDAP, WHOIS,
crt.sh, CertStream, PhishTank). No paid reputation services are in scope for
now. The throttle system (Agents.md §5.6) generalizes rate + quota limits so
free-tier caps are respected; its shape is deliberately reusable for paid caps
later without changing the model.

## Consequences

- Keeps the dependency and secret surface small; no paid credentials required.
- Detection must derive all signals from the free sources (similarity,
  bulk-registration / registrar overlap, CT volume, homoglyph use) — no
  external reputation score.
- Revisit (new ADR) if a paid source proves necessary after the core loop
  ships.
