# GoPhish

**Defensive OSINT tool for detecting newly registered and suspicious URLs used
in smishing (SMS phishing) and phishing campaigns.**

GoPhish helps defenders answer three questions:

1. **What domains were just registered** that look like they belong to a
   brand or target we care about?
2. **What look-alike / bulk-registered URLs exist** (typosquats, homoglyphs,
   TLD swaps, sibling registrations) similar to a known legitimate domain?
3. **Which of these are actually malicious or suspicious**, and why — using
   deterministic signals plus a local LLM for narrative analysis.

> **Defensive-only scope.** This tool is for authorized security research,
> brand protection, and incident response. It must never generate, host, or
> deliver phishing content, nor automate attacks. It produces *detections and
> analysis*, not operational phishing infrastructure.

---

## Status

This repository is an **early skeleton**. The current code:

- Loads configuration (TOML) describing external OSINT sources.
- Launches a minimal **terminal UI** (Bubble Tea) that displays a
  "hello world" status screen listing the configured services and their
  enabled state. Press `q` to quit.

The full pipeline described below (`sources → fuzz → detect → llm`), the
remaining TUI views, and storage are **planned but not yet implemented**.
See [`AGENTS.md`](./AGENTS.md) for the full design and [`docs/adr`](./docs/adr)
for recorded decisions.

## Getting started

### Requirements

- Go 1.22+

### Build & run

```bash
go build -o gophish ./cmd/gophish
./gophish
```

### Configuration

Copy the example config and edit it:

```bash
cp config.example.toml gophish.toml
```

Each source is **config-driven** — you can add, remove, or swap an endpoint in
`gophish.toml` without rebuilding. A service listed in the file replaces its
default entry entirely; services not mentioned keep their defaults.

```toml
[services.phishtank]
base_url = "https://data.phishtank.com/data"
enabled = true
requests_per_minute = 10
api_key_env = "PHISHTANK_API_KEY"   # read from env, never hardcoded
```

Config keys per source:

| Key                  | Purpose                                              |
| -------------------- | ---------------------------------------------------- |
| `base_url`           | Endpoint used by the source client.                  |
| `api_key`            | Optional credential (prefer `api_key_env`).          |
| `api_key_env`        | Env var that, if set, overrides `api_key`.           |
| `enabled`            | Toggle the source on/off.                            |
| `requests_per_minute`| Per-source rate limit (see §5.6 in AGENTS.md).       |
| `max_checks`        | Hard quota ceiling for this source.                  |
| `quota_window`       | Duration after which the quota counter resets.       |

## Architecture (planned)

```text
   seed domain
        │
        ▼
   sources ──► RDAP / WHOIS, Certificate Transparency (crt.sh, CertStream),
        │       PhishTank  →  freshly registered & certified domains
        ▼
   fuzz    ──► generate look-alike candidates (typosquat, homoglyph,
        │       TLD swap, permutations)
        ▼
   detect  ──► score & cluster candidates (similarity, bulk-registration
        │       clusters, weighted risk score)
        ▼
   llm     ──► summarize clusters into a campaign narrative + explain risk
```

See [`AGENTS.md`](./AGENTS.md) for the complete design, conventions, and
security guardrails.

## Security & legal guardrails

1. **Authorized use only** — operators need a legitimate defensive remit.
2. **Read-only OSINT** — only public data sources and local resolution.
3. **No phishing generation** — the fuzzer produces detection candidates only.
4. **Rate limiting & politeness** — honor each source's ToS, rate, and quota.
5. **Data minimization** — cache only what's needed; provide purge.
6. **Provenance** — every detection includes its evidence and source, and
   low-confidence results are labeled "suspicious, unverified."
