# POC-0003 — Gname bulk-registration mechanics + campaign intelligence for the `gov-<rand>.fit` family

Status: **complete** (research spike; no live queries executed)
Depends on: POC-0002 (live seed test, 0/34 fuzz yield)
Seed context: `hawaii.gov-sdas.fit` (POC-0002)

This POC is a **research spike**, not a live test. It answers two questions left
open by POC-0002's zero-yield result: (1) how does Gname bulk registration
actually work, and does it explain how the scammer generates domains?
(2) is this campaign documented anywhere, and what does that tell us about the
real generation pattern? All information is from public sources; no domains,
registrars, or infrastructure were queried for this POC.

---

## 1. How Gname bulk registration works

Sources: Gname batch-search page and API docs (§5, S1–S6).

### 1.1 Web batch tool — list in, availability out

`https://www.gname.com/batch/search` accepts a **pasted list of domains**,
checks availability across ~500 supported suffixes (`.fit`, `.top`, `.xyz`,
`.vip`, `.icu`, `.cfd`, `.sbs`, `.quest`, `.one` … all present), filters
unsupported suffixes, and registers what is available. **There is no
name-generation feature.** The tool is purely "list in → register what
resolves as available".

### 1.2 API — the scalable path a scammer would use

| Endpoint | Function | Notes |
| -------- | -------- | ----- |
| `/api/buynow/check/bulk` | Bulk availability check | comma-separated domain list |
| `/api/buynow/buy/bulk` | Bulk purchase | domain\|price pairs |
| `/api/backorder/bulk` | Bulk backorder | newline-separated, **max 100 domains per submission** |
| `/api/domain/reg` | Single registration | scripted |

Auth is APPID + UNIX timestamp + signature (`gntoken`) — designed for
server-to-server automation.

### 1.3 Implication for the "regex generation" hypothesis

Gname generates **nothing**. The pipeline is:

```text
scammer-side script (random token generator / regex)
   →  candidate list
   →  Gname bulk check API
   →  register available ones (cheap TLD, ~1 USD each)
```

This confirms POC-0002's interpretation: the token-generation logic lives
entirely in the operator's tooling and is **invisible to us** except through
its outputs (registered domains). We cannot enumerate the token space from the
outside; we can only recognize the *shape* of outputs.

### 1.4 Gname's abuse posture (context, not accusation)

Independent measurement (S8, arXiv 2510.14198, 67,907 confirmed toll-scam
domains): three registrars handled **91.8%** of toll-scam registrations —
Dominet (HK) 72.9%, NameSilo 10.8%, **Gname 8.1%** — vs. only 3–6% of
legitimate registrations (p < 0.000001). High-risk TLDs: `.xin`, `.top`,
`.vip`, `.world`, `.win` (86.9% of that dataset). Gname operates a public
abuse-reporting channel (`gname.com/abuse`, IANA registrar ID 1923, listed by
phish.report) — relevant for the takedown/reporting step, not detection.

## 2. Campaign intelligence — the seed's family is documented

### 2.1 Direct match: the Hawaii DMV smishing deep-dive

Source S9 (researcher write-up, ~5 days before this POC) reverse-engineered
`hawaii.gov-wwa.fit` — **the same domain shape as our seed
`hawaii.gov-sdas.fit`** — as part of the "15C-16.003" DMV/toll smishing wave
documented by state agencies in 12+ US states and Alberta since May 2025
(S10–S14). The kit is internally named **"Sailors"**; TTPs overlap the
Lucid/Darcula/Smishing-Triad PhaaS ecosystem documented by Prodaft/Unit42/
Netcraft (S15–S17).

### 2.2 The real generation template

> `hawaii.[gov|org]-[random word].[cheap TLD]/dmv`

Observed registrable domains: `gov-wwa.fit`, `org-nqa.one`, and our
`gov-sdas.fit`. Properties that invalidate POC-0002's fuzz design:

1. **Tokens are independent random strings** (`wwa`, `nqa`, `sdas`) — not
   anagrams, not keyboard walks, sharing no lexical relationship. Our 35-token
   anagram/walk payload could never have hit. Yield 0/34 was structural, not
   bad luck.
2. **The prefix alternates** `gov-` / `org-`. POC-0002 tested `gov-` only
   (in-scope per the fixed hypothesis, but the family is wider).
3. **TLDs rotate** across cheap suffixes (`.fit`, `.one`, …).
4. **Just-in-time registration:** `gov-wwa.fit` was registered **8 hours
   before** the first lure citing it. 7 domains in 10 days, each burned and
   replaced. There is no large pre-registered pool sitting in RDAP waiting to
   be enumerated — the fleet is created and discarded faster than manual
   fuzzing cycles.

### 2.3 Infrastructure & detection-relevant traits

| Trait | Value | Detection consequence |
| ----- | ----- | --------------------- |
| Hosting | Alibaba Cloud **AS45102** (Virginia), single server, short TTLs | NS/IP/ASN pivot beats lexical guessing |
| DNS (our seed) | `a.share-dns.com` / `b.share-dns.net` | shared-DNS co-tenancy pivot |
| CT presence | **none** (confirmed POC-0002 §3.3 and S9) | **crt.sh is blind to this campaign** — do not rely on CT as primary signal for it |
| C2 | same domain, `wss://<domain>/console/` | takedown of domain kills C2 (good for reporting) |
| Scanner evasion | mobile-User-Agent gating; desktop/scanners get 404 | **justifies POC-0001's "never fetch candidates" rule** — fetching is both non-compliant and useless here |
| Kit fingerprints | AES keys `ZQMWLSPXJRDHKTNV` / `YFBCUENAGPQLXJWR`, `/console/` path, `changleField` typo | pivot IOCs for researchers with netflow/sandbox visibility |

## 3. Revised detection strategy (input to Plan.md §4.8 mini loop)

POC-0002 said "don't build blind token enumeration; build infra pivots".
This POC sharpens **how**:

```text
feed: newly-registered-domain (NRD) stream / RDAP spot checks
  │
  ▼ lexical filter (the fuzzer's real job — a recognizer, not a generator)
  ^(gov|org)-[a-z]{2,6}\.(fit|one|xin|top|vip|world|win|xyz|icu|cfd|sbs|quest)$
  │
  ▼ RDAP corroboration (throttled — POC-0002 §3.5: < ~90 req/100s to rdap.org)
  registrar ∈ {Gname, Dominet HK, NameSilo}  AND  age < 72h
  │
  ▼ infra pivot
  NS ∈ {*.share-dns.*, Alibaba AS45102}  →  confidence up
  │
  ▼ output
  "suspicious, unverified" until corroborated (AGENTS.md §7.6)
```

Consequences for the codebase:

- `internal/fuzz`: the permutation engine (alterx-style cross-product) is
  still useful for **brand-keyword** shapes and for expanding *known* tokens,
  but the `.fit`-family detector is a **regex/score function** in
  `internal/detect`, applied to an inbound feed — not a candidate generator.
- `internal/source`: prioritize an NRD/RDAP source for this family; **CT is
  deprioritized** (this campaign mints no certs). crt.sh flakiness (POC-0002
  §3.3) matters less than its coverage gap here.
- `internal/detect/score.go`: candidate signals with observed priors —
  registrar (Gname/Dominet/NameSilo), age < 72h, cheap-TLD list, `gov|org-`
  prefix + short random label, share-dns/Alibaba NS. CT absence must **not**
  reduce score (POC-0002 §3.3).
- Reporting path: registrar abuse channel exists (Gname `/abuse`); domain
  takedown also kills the C2 (§2.3).

## 4. Verdict

- POC-0002's zero yield is **explained and externally validated**: tokens are
  independent randoms, registered just-in-time; enumeration is the wrong
  primitive.
- The detection win is a **shape recognizer over NRD/RDAP + registrar/age/NS
  scoring**, all within free/freemium sources (AGENTS.md §1).
- alterx remains the right tool for the *other* fuzz families
  (combosquats on a real brand); no change to POC-0001 §4's lean
  (Go-native `internal/fuzz`).

## 5. Sources

| # | Source | Used for |
| - | ------ | -------- |
| S1 | `gname.com/batch/search` | batch tool mechanics, supported TLD list (incl. `.fit`) |
| S2 | `gname.com/.../api/buynow/bulkcheck` | bulk availability API |
| S3 | `assets.gname.com/.../api/buynow/bulkbuy` | bulk purchase API |
| S4 | `assets.gname.com/.../api/backorder/bulk` | 100-domains-per-submission limit |
| S5 | `gname.com/us/domain/api/domain/reg` | scripted single registration |
| S6 | `gname.vip/zhcn/help/202104281510531953.html` | bulk backorder workflow |
| S7 | `gname.com/abuse`, `phish.report/contacts/GNAME` | abuse channel, IANA ID 1923 |
| S8 | arXiv:2510.14198, *Infrastructure Patterns in Toll Scam Domains* | registrar/TLD concentration stats |
| S9 | Medium/@emuzzi19, *Reverse Engineering the Hawaii DMV Text Scam* (via web.archive.org) | campaign template, timing, infra, IOCs |
| S10 | governor.hawaii.gov cybersecurity alert | same-family Hawaii impersonation (codify.inc) |
| S11 | hidot.hawaii.gov blog 2026-03-17 | Hawaii DOT scam-text warning |
| S12 | kauai.gov press release / govdelivery bulletin | "15C-16.003" DMV smish warnings |
| S13 | cyber.nj.gov, *SMiShing at Scale* | state-toll smishing TTPs |
| S14 | silentpush.com/blog/imp-1g-smishing | US/CA gov smishing actor tracking |
| S15 | unit42.paloaltonetworks.com/global-smishing-campaign | Smishing Triad, 136,933 root domains |
| S16 | darkreading.com (Smishing Triad) | 194k+ FQDNs, scale |
| S17 | cyberscoop.com (Smishing Triad) | PhaaS ecosystem context |
| S18 | interisle.net *Criminal Abuse of Domain Names* + substack | bulk-registration abuse background |

## 6. Compliance note

This POC is pure desk research on public reporting, registrar documentation,
and academic literature (AGENTS.md §7.2 read-only OSINT). No candidate,
seed, or sibling domain was queried or contacted; no registrar API was
exercised. IOCs quoted from S9 (AES keys, socket path, code typo) are
defensive hunting pivots published by the original researcher for that
purpose. Nothing here generates or facilitates phishing content (§7.3), and
the detection strategy labels uncorroborated matches "suspicious,
unverified" (§7.6).
