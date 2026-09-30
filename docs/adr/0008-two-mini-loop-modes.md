# ADR-0008: Two mini-loop modes — generation and feed-recognition

- Status: Accepted
- Date: 2026-09-30

> Numbering note: ADR-0004 through ADR-0007 are reserved by the milestone
> plan (Plan.md §5.3, §5.4, §5.5, §5.8) for the fuzzer, source+throttle,
> detection, and LLM subsystems. This decision was made before those
> milestones close, so it takes the next unreserved number.

## Context

The core loop (Plan.md §4.3) and mini loop (§4.8) as originally written
assume a single operating mode: **generate candidates from a seed, then
source-check them** (generate-then-check).

Three POCs tested that assumption against a real reported scam seed
(`hawaii.gov-sdas.fit`, registrar Gname):

- POC-0001 established the manual pipeline mechanics (alterx cross-product,
  RDAP evidence extraction).
- POC-0002 ran the live test: 35 `gov-<rand>.fit` candidates → **0/34 novel
  hits** (the one 200 was the seed itself). RDAP 200/404 confirmed as the
  registration primitive; rdap.org throttles at ~90 req/100 s; the seed has
  **no CT certificates**.
- POC-0003 (desk research) explained the zero yield: the campaign family
  registers `hawaii.[gov|org]-[random].[cheap TLD]` domains **just-in-time**
  (hours before first lure) via Gname's bulk API, with **independent random
  tokens** (`wwa`, `nqa`, `sdas` share no lexical relationship) and no TLS
  certificates. Enumeration of such token spaces is structurally impossible
  (~26⁴ and the tokens are not related); blind permutation fuzzing has
  ~zero yield for this family.

## Decision

The mini loop (Plan.md §4.8) operates in **two modes**:

- **Mode A — generation (existing, default).** Seed → generate candidates
  (typosquat, homoglyph, TLD-swap, brand-keyword combosquat, permutations of
  *known* tokens) → source-check → expand around confirmed hits. Unchanged.
- **Mode B — recognition (new).** An inbound newly-registered-domain (NRD)
  or RDAP stream is filtered by **campaign-shape recognizers** (regexes,
  e.g. `^(gov|org)-[a-z]{2,6}\.(fit|one|xin|top|vip|world|win)$`), then
  corroborated via RDAP (registrar, age < 72h) and infra pivots
  (nameserver/ASN co-tenancy). The recognizer lives in `internal/detect`;
  the NRD feed is a `Source` added in Milestone 8.

Detection scoring adopts the evidence-backed priors from POC-0003:
registrar concentration, registration age, cheap-TLD list, `gov|org-`
prefix shape, NS/ASN pivots. **CT absence must never lower a score** — this
campaign family mints no certificates, so CT is a corroborating signal
only, not a primary one, for cert-less campaigns.

## Consequences

- `internal/fuzz` (Milestone 2) is unchanged in scope — Mode A only. Its
  design is *validated* for the families it targets (brand-keyword shapes
  are the dominant pattern in smishing infrastructure, POC-0001 §1.5).
- `internal/detect` (Milestone 4) gains the recognizer responsibility; its
  scoring weights now have empirical priors instead of guesses (POC-0002
  §3.3 goal achieved).
- Milestone 8 gains an NRD-feed `Source` (Mode B's input) — still
  free/freemium, consistent with ADR-0001.
- Throttle defaults informed by measurement: rdap.org ≲ 90 req/100 s per IP
  before 403s (POC-0002 §3.5).
- YAGNI preserved: Mode B is regex + scoring over a feed — no new
  foundational dependencies.
- Guardrails unaffected: recognition queries public registry data only; no
  candidate domain is ever contacted (POC-0001 §3.2); uncorroborated
  matches stay "suspicious, unverified" (AGENTS.md §7.6).
- The POC-0002 RDAP positive/negative bodies become `.fit` parsing fixtures
  for Milestone 3.
