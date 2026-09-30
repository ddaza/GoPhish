# POC-0001 — Manual URL query proof of concept

Status: **in progress** (discovery phase complete, execution not started)
Scope: validate the `seed → fuzz → check → report` loop **manually**, using
`alterx` for candidate generation and public OSINT endpoints for evidence,
before writing any Go in `internal/fuzz` / `internal/source` / `internal/detect`.

This is a spike, not a milestone. It de-risks Milestones 2–4 (Plan.md §5.3–§5.5).
No production Go code changes. No ADR yet — an ADR becomes necessary only if we
decide to adopt an external fuzzer binary permanently (see §6).

---

## 1. What we did

Five read-only probes against the local toolchain and public endpoints.

### 1.1 Toolchain check

| Tool   | Status                    | Path                    |
| ------ | ------------------------- | ----------------------- |
| alterx | installed                 | `~/go/bin/alterx`       |
| curl   | installed                 | `/mingw64/bin/curl`     |
| jq     | installed                 | winget `jqlang.jq`      |
| go     | installed                 | `C:/Program Files/Go`   |
| dig    | installed                 | winget `ISC.Bind`       |
| whois  | installed                 | winget `Sysinternals`   |

Environment is Git Bash on Windows. Everything the POC needs is present.

### 1.2 Probe A — alterx default output

```bash
echo "example.com" | alterx -silent
```

**Result:** 111 permutations, **all subdomain permutations** —
`login.example.com`, `dev.example.com`, `cdn.example.com`, …

**Finding:** alterx is a *subdomain* wordlist generator. Its default patterns do
**not** produce what GoPhish needs. GoPhish needs look-alike **registrable
domains** (`example-verify.com`, `examp1e.com`, `example.co`) — those are what
an attacker actually registers. Default alterx output is the wrong shape.

### 1.3 Probe B — reverse-engineering the alterx DSL

```bash
echo "login.example.com" | alterx -silent -p '{{sub}}|{{suffix}}|{{word}}' -pp 'word=probe'
# → login|example.com|probe

echo "example.com" | alterx -silent -p '{{sub}}|{{suffix}}|{{word}}' -pp 'word=probe'
# → (no output)
```

**Findings:**

- Template variables are `{{sub}}`, `{{suffix}}`, `{{word}}`, `{{number}}`,
  `{{region}}` (per `~/.config/alterx/permutation_v0.1.0.yaml`).
- `{{suffix}}` = the **registrable domain** (`example.com`), `{{sub}}` = the
  leftmost label.
- **Custom payload names are allowed** via `-pp 'name=file.txt'` — this is the
  lever that makes the POC work.
- For a bare 2-label seed, `{{sub}}` is **empty**, so any pattern referencing it
  yields an empty string and is dropped. That is why Probe A on `example.com`
  still produced output (it uses `{{word}}.{{suffix}}`) but Probe C produced none.

### 1.4 Probe C — the blocking limitation

```bash
echo "example.com" | alterx -silent -p '{{suffix}}-{{word}}.{{tld}}' \
  -pp 'word=kw.txt' -pp 'tld=tlds.txt'
# → example.com-verify.com      ← malformed
```

**Finding — two hard limits:**

1. **No label/TLD split.** `{{suffix}}` always carries the TLD, and the DSL has
   no way to strip it. Naive TLD-swap patterns produce malformed domains.
2. **No character-level transformation.** The DSL is pure string composition
   over payload lists. There is no substitution function, so **typosquats
   (`examp1e.com`) and homoglyphs (`еxample.com`) are not expressible in alterx.**

### 1.5 Probe D — the validated workaround

Split label/TLD in the shell, then feed them to alterx as payloads so it does
what it is genuinely fast at — the cross-product.

```bash
printf 'example\n'                                   > stem.txt
printf 'com\nnet\ninfo\nxyz\nco\n'                    > tlds.txt
printf 'verify\nsecure\naccount\nlogin\nbilling\n'    > kw.txt

cat > lookalike.patterns <<'EOF'
{{stem}}-{{word}}.{{tld}}
{{stem}}{{word}}.{{tld}}
{{word}}-{{stem}}.{{tld}}
{{word}}{{stem}}.{{tld}}
{{stem}}.{{tld}}
EOF

echo "example.com" | alterx -silent -p lookalike.patterns \
  -pp 'stem=stem.txt' -pp 'tld=tlds.txt' -pp 'word=kw.txt' | sort -u
```

**Result:** 21 clean, deduped, **registrable** look-alikes:

```text
example-verify.com     verifyexample.net      account-example.info
example-billing.com    secure-example.xyz     login-example.co
examplelogin.com       examplesecure.com      …
```

**Finding:** this is the right division of labor. alterx = combinatorial engine;
the shell = token preparation. These `brand + keyword` shapes are the dominant
pattern in real smishing infrastructure, so the signal is genuinely useful even
without typosquats.

### 1.6 Probe E — RDAP evidence extraction

```bash
curl -sS -L "https://rdap.org/domain/example.com"
```

**Result:** HTTP 200. Extracted with jq:

| Field       | Value                                            |
| ----------- | ------------------------------------------------ |
| handle      | `2336799_DOMAIN_COM-VRSN`                        |
| registration| `1995-08-14T04:00:00Z`                           |
| expiration  | `2027-08-13T04:00:00Z`                           |
| last changed| `2026-08-14T08:01:43Z`                           |
| registrar   | `RESERVED-Internet Assigned Numbers Authority`   |
| nameservers | `ELLIOTT.NS.CLOUDFLARE.COM`, `HERA.NS.CLOUDFLARE.COM` |

**Finding:** RDAP maps **1:1 onto `analyze.Result`** (Plan.md §4.2) —
`Registered`, `Registrar`, `Nameservers`, `CreatedAt`, plus `Raw` for status and
DNSSEC. `rdap.org` bootstraps to the authoritative registry, so one URL pattern
covers all TLDs. No scraping, structured JSON, rate-friendly — matches
Plan.md §13's "prefer RDAP over legacy WHOIS".

---

## 2. What we did NOT yet do

These were the next probes when we paused:

1. **RDAP negative case** — confirm HTTP 404 on an unregistered domain. This is
   the *core detection primitive*: `404 = not registered`, `200 = registered`.
   Must be verified before anything else, because the whole POC's signal depends
   on it, and some registries return 200 with an error body or a redirect instead.
2. **crt.sh CT lookup** — `https://crt.sh/?q=<domain>&output=json`. Certificate
   Transparency is the "primary early signal" in Plan.md §13: a freshly issued
   cert on a look-alike domain means infrastructure is being stood up, often
   *before* the domain is weaponized.
3. **An end-to-end run** on a real seed.
4. **Report generation.**

---

## 3. What we are thinking about doing now

### 3.1 Shape: a staged shell pipeline, zero Go changes

```text
stage 1  seed                → stem/tld split  (shell, trivial)
stage 2  stem × kw × tld     → candidates      (alterx cross-product)
stage 3  candidates          → RDAP evidence   (curl, throttled, capped)
stage 4  evidence            → findings        (jq: age, registrar, NS clustering)
stage 5  findings            → report          (markdown + JSON, with provenance)
```

Artifacts under `poc/` (gitignored by default, see §5 Q5):

```text
poc/
  patterns/lookalike.patterns
  payloads/{stems,tlds,keywords}.txt
  run.sh                    # driver: --seed --max --sleep --out
  out/<seed>/{candidates.txt,evidence.jsonl,findings.json,report.md}
```

Each stage writes a file, so any stage can be re-run or inspected alone. That
is the point of a *manual* POC — we look at the intermediate artifacts.

### 3.2 Guardrails baked into the POC (AGENTS.md §7)

These are non-negotiable and go into `run.sh`, not into a README footnote:

- **Never fetch the candidate domains themselves.** No `curl https://example-verify.com`.
  Visiting a look-alike is active probing of third-party infrastructure and it
  leaks our interest to the operator. Evidence comes from **RDAP and CT only** —
  both are passive lookups of public registry/log data. This is the single most
  important rule in the POC.
- **Rate limit + hard cap.** `sleep` between RDAP calls (default ~2s), `--max`
  caps total lookups (default 50), abort-on-quota rather than hammer. Mirrors
  `internal/throttle` semantics (Plan.md §4.6) so the POC teaches us real numbers.
- **Custom User-Agent** identifying the tool as defensive research.
- **Provenance on every record** — source, timestamp, query — written into
  `evidence.jsonl` (AGENTS.md §7.6).
- **"suspicious, unverified" labeling** on anything not corroborated (§7.6).
- **Defensive-only output.** alterx patterns generate *detection candidates*.
  The report contains no lure text, no message templates, no payloads (§7.3).
- **Data minimization.** Store only what the analysis needs; `poc/out` is
  disposable (§7.5).

### 3.3 What the POC is actually measuring

Not "does the pipeline run" — it is a spike to answer four questions:

1. **Is the RDAP 404/200 split reliable enough to be the registration signal?**
   Across TLDs, including ccTLDs that may not support RDAP.
2. **What is the hit rate?** Of N generated look-alikes, how many are registered
   at all, and how many are *recently* registered? This tells us whether
   alterx-style compositional fuzzing is worth wiring into `internal/fuzz`, or
   whether the yield is too low to justify the lookup budget.
3. **Which detection signals actually separate signal from noise?** Domain age,
   registrar reuse across siblings, shared nameservers, brand-keyword presence.
   We eyeball this manually now so `internal/detect/score.go` weights are
   informed by evidence rather than guesses.
4. **What do real RDAP responses look like?** So we can capture them as fixtures
   under `test/` for the Milestone 3 parsing tests (AGENTS.md §6).

### 3.4 Direct payoff per milestone

| Milestone | What the POC hands it |
| --------- | --------------------- |
| 2 — Fuzzer | Proof that alterx covers **compositional** look-alikes but **not** char-level typos/homoglyphs. `internal/fuzz` still needs `typosquat.go` + `homoglyph.go` regardless. Also: a validated pattern/payload set to port. |
| 3 — Source + throttle | Real RDAP JSON fixtures for `test/`; confirmed `Result` field mapping; measured rate-limit numbers for `requests_per_minute` defaults. |
| 4 — Detection | Observed signal distributions to weight `score.go`; real examples of registrar/NS clustering for `bulkreg.go`. |
| 5 — Mini loop | Evidence on whether "expand around confirmed hits" (same registrar) finds anything in practice. |

---

## 4. The key architectural finding so far

**alterx cannot be a drop-in `Fuzzer` implementation.** It is a strong
combinatorial engine for `{stem}-{keyword}.{tld}` shapes, but the DSL has no
character-level transformation, so typosquats and homoglyphs — two of the four
fuzz types named in Plan.md §13 — are out of reach.

Three options, to decide **after** the POC produces data:

- **(a) Pure Go `internal/fuzz`** — implement all four fuzz types natively as
  planned. alterx informed the pattern list but is not a dependency.
- **(b) alterx as one `Fuzzer` adapter among several** — shell out for
  compositional patterns, Go for char-level. Requires an ADR (implements a core
  interface, AGENTS.md §8) and adds a build/runtime dependency on an external
  binary, which sits awkwardly with "single binary" and "no network at build time".
- **(c) Port alterx's pattern/payload model into Go** — keep the DSL idea
  (patterns × payload cross-product) but implement it in `internal/fuzz`.

Current lean: **(a) or (c)**. The POC's value is the *pattern list and the
measured yield*, not the binary. But we decide on evidence, not now.

---

## 5. Open questions (need answers before continuing)

1. **Which seed?** The POC needs a real target you have a legitimate defensive
   remit for (own brand, employer, sanctioned research — AGENTS.md §7.1).
   `example.com` is fine for mechanics but yields no interesting signal since it
   is IANA-reserved.
2. **Shell POC first, or go straight to Go?** Recommendation: **shell first** —
   it is a spike, and the intermediate artifacts are the deliverable. Writing Go
   now would mean designing `internal/fuzz` before we know the yield.
3. **Source scope:** RDAP only, or RDAP + crt.sh? Recommendation: **RDAP first**
   (Probe E already proves it), add crt.sh as a second pass — CT is the earlier
   signal but crt.sh is slower and less reliable.
4. **Lookup budget:** what `--max` and `--sleep` are acceptable for your first
   real run? Recommendation: `--max 50`, `--sleep 2`.
5. **Are `poc/` artifacts committed or gitignored?** The candidate lists and
   evidence are derived OSINT about third parties. Recommendation: **gitignore
   `poc/out/`**, commit only `poc/patterns/`, `poc/payloads/`, `poc/run.sh`.
6. **Report format:** markdown for humans, JSON for later reuse as fixtures?
   Recommendation: both, from the same `findings.json`.

---

## 6. Compliance note

Nothing in this POC generates, hosts, or delivers phishing content. Candidate
generation is for **detection**; all evidence gathering is read-only queries
against public registry (RDAP) and certificate transparency (CT) data; no host
is scanned, no look-alike domain is contacted, no paid service is used.
Every finding carries provenance and low-confidence results are labeled
"suspicious, unverified" to avoid wrongful takedown claims.
