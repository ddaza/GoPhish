# ADR-0002: Configuration Implementation Strategy

- Status: Proposed
- Date: 2026-07-20

## Context
GoPhish requires a robust and flexible configuration mechanism to manage its various subsystems (§5). Specifically, we need to configure:
1.  **Data Sources**: API keys (e.g., PhishTank), rate limits, and quota caps for each source (§5.1, §5.6).
2.  **Fuzzing**: Maximum bounds for candidate generation (e.g., `--max 5000` in §5.2).
3.  **LLM**: Default model name and API endpoint (e.g., Ollama URL in §5.4).
4.  **Throttling**: Per-source rate limits (RPM) and total quotas (§5.6).
5.  **CLI**: Allow users to override settings via command-line flags (via `Cobra` in §2).

The current implementation in `internal/config/config.go` is the central point for this logic.

## Decision
We will use the **Viper** library to handle configuration loading. Viper will be configured to read settings in the following order of precedence (highest to lowest):
1.  Command-line flags (via `Cobra`).
2.  Environment Variables (prefixed, e.g., `GOPHISH_LLM_MODEL`).
3.  A configuration file, defaulting to `config.toml` in the root directory.

The configuration structure will be defined in `internal/config/config.go` and exposed via a singleton or context-aware struct.

## Consequences
**Easier:**
*   Operators can easily tune the tool without recompiling.
*   The CLI provides a natural way to override defaults for specific runs.
*   The system is highly adaptable to different deployment environments (local vs. containerized).

**Harder:**
*   Requires careful management of environment variable naming conventions to avoid collisions.
*   The initial setup in `config.go` must correctly initialize and bind all expected keys from the TOML file and CLI flags.

**Consistency Check:**
*   **YAGNI Scope (§1):** This approach is minimal and directly supports the core loop's needs.
*   **Security Guardrails (§7):** Configuration secrets (like API keys) are read from the environment/file and are never hardcoded, adhering to the principle of data minimization and secure handling.