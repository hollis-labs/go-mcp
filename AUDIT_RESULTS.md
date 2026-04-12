# Audit — go-mcp (MCP Helpers)

**Audited:** 2026-04-09
**Auditor:** general-purpose subagent (BOOT_STANDARDIZATION audit)
**Path:** libs/go-mcp
**Kind:** lib

## Summary

`libs/go-mcp` is a small, stdlib-only Go module exposing a single subpackage
`budget/` that implements the MCP list-response envelope used by Fragments
Engine MCP servers. The code itself is coherent and tested, and the release
baseline now includes a LICENSE, package `doc.go`, README, and runnable
examples. The remaining gaps are mostly repo-hygiene and scope decisions: no
CHANGELOG, and session/state artifacts still live in the root alongside the
module files.

## Checklist

| # | Check | Status | Notes |
|---|---|---|---|
| 1 | `go.mod` present | pass | Module: `github.com/hollis-labs/mcp-helpers`, Go 1.25.0 |
| 2 | `README.md` present (before this audit) | fail | No README existed before this audit. Newly written. |
| 3 | `LICENSE` present | pass | `LICENSE` added with MIT text. |
| 4 | `doc.go` with `// Package X ...` godoc comment | pass | Package doc now lives in `budget/doc.go`. First sentence: "Package budget provides MCP response budget enforcement utilities." |
| 5 | Module path matches intended repo layout | pass | `go.mod` declares `github.com/hollis-labs/mcp-helpers`; per D2, the standalone module keeps its declared canonical path and the README/docs now match the actual import surface. |
| 6 | README has standard sections (title, desc, install, usage, API, examples) | pass | Newly authored README follows BOOT_STANDARDIZATION template. |
| 7 | Tests exist (`*_test.go`) | pass | 3 test files in `budget/`: `budget_test.go`, `helpers_test.go`, `tokens_test.go`. Coverage not measured. |
| 8 | Examples (`example_test.go` or `examples/`) | pass | Added `budget/example_test.go` with runnable examples for `Apply`, `ToolJSON`, and `ExtractPagination`. |
| 9 | State/session files NOT misclassified as library docs | pass | `.agentrc/`, `CLAUDE.md`, `agentrc.yaml`, `.agentrc/boot/`, `.agentrc/bootstrap.md`, `.agentrc/agent-boot.md` are present but excluded from README. |
| 10 | Public API sanity: errors typed/sentinel, `context.Context` first arg | partial | No sentinel/typed errors exposed; `ToolError` returns a stringified JSON error (acceptable for an MCP-wire helper). No function in the public API accepts a `context.Context`, but none performs I/O or long-running work, so the omission is defensible. Flagged as partial for visibility. |
| 11 | `CHANGELOG.md` present (nice to have) | fail | No CHANGELOG. |
| 12 | No circular/suspicious deps on other framework libs | pass | Zero non-stdlib imports. No framework-internal dependencies. |

## Findings — Required Fixes

1. **Add a LICENSE file.**
   - **What:** The library has no `LICENSE` file.
   - **Why:** Without a license, the code is not legally redistributable; blocks publishing or external use.
   - **Suggested fix:** Add the framework's standard LICENSE (MIT).
   - **Status:** Resolved in this apply session.

2. **Reconcile the `go.mod` module path with the framework layout.**
   - **What:** `go.mod` declares `module github.com/hollis-labs/mcp-helpers`, but the code lives at `framework/libs/go-mcp`.
   - **Why:** Consumers inside the framework will import by a path that does not match the repo, and callers outside the framework will get a 404. This is the single biggest release blocker.
   - **Suggested fix:** Decide on a canonical module path (e.g., `github.com/<framework-org>/framework/libs/go-mcp`) and update `go.mod` plus any downstream imports in a follow-up non-audit session.
   - **Status:** Resolved by D2. The module remains a standalone repo with its declared canonical path, and the README/docs now describe the actual import surface without implying a framework-monorepo path.

3. **Add a top-level `doc.go` for the module.**
   - **What:** There is no package doc at the module root; the only godoc comment sits inside `budget/envelope.go`.
   - **Why:** Tools like `pkg.go.dev` and `go doc` show nothing for the module root, making discovery harder.
   - **Suggested fix:** Add `doc.go` at the repo root (or promote the `budget` comment into a canonical `budget/doc.go`) explaining the module is a collection of MCP helpers and currently contains only `budget/`.
   - **Status:** Resolved by promoting the comment into `budget/doc.go`.

4. **Remove session/state files from the library root or move them under an ignored path.**
   - **What:** `.agentrc/`, `CLAUDE.md`, `agentrc.yaml`, `.golangci.yml`, `lefthook.yml` all sit alongside `go.mod`. Several are agent-session artifacts, not library assets.
   - **Why:** They confuse the "what is this directory?" question for a human or `go get` user and violate BOOT_STANDARDIZATION rule 2.
   - **Suggested fix:** At minimum, add a note in a `CONTRIBUTING.md` (or README "development" section) explaining what `.agentrc` is; ideally relocate agent state out of the published module. Keep `lefthook.yml` and `.golangci.yml` only if this module is meant to be linted in isolation.
   - **Status:** Deferred. These files are repo-local tooling/state and were left untouched in this apply pass.

5. **Decide whether `go-mcp` is a multi-subpackage namespace or should be renamed `go-mcp-budget`.**
   - **What:** Directory name `go-mcp` implies a broader MCP toolkit, but only `budget/` exists.
   - **Why:** README and API overview have to keep apologizing for the mismatch.
   - **Suggested fix:** Either (a) commit to `go-mcp` as a future umbrella and leave a placeholder note, or (b) rename the directory and module path to reflect that this is specifically the budget helper.
   - **Status:** Resolved in docs. The module is documented as a one-subpackage namespace today, and no rename is required for the standalone repo model.

## Findings — Nice-to-Have

1. **Add an `example_test.go` under `budget/`.**
   - **What:** No runnable godoc examples exist.
   - **Why:** `pkg.go.dev` will render `Example*` functions directly; the README snippet was hand-written and is not compile-checked by `go test`.
   - **Suggested fix:** Add `ExampleApply`, `ExampleToolJSON`, and `ExampleExtractPagination` based on the patterns in `budget_test.go`.
   - **Status:** Resolved in this apply session.

2. **Enforce `MaxBytes` / `MaxTokens` in `budget.Apply` (or rename them).**
   - **What:** `Config.MaxBytes` and `Config.MaxTokens` are plumbed through `withDefaults` but never consulted in `Apply` — truncation is solely driven by `Limit`.
   - **Why:** Users reasonably expect these fields to cap serialized size. Silent no-op is worse than omission.
   - **Suggested fix:** Either (a) serialize included items, walk the byte count, and pop items until the budget fits, or (b) delete the fields and document that size enforcement is the caller's job.

3. **Add `CHANGELOG.md`** once the API has a first tagged release.

4. **Reference or inline ADR-006** so the "canonical contract" the package doc points to is discoverable from the library alone.

5. **Document the `EstimateTokens` heuristic** more explicitly (4 chars/token is OK for JSON; note it underestimates long natural-language strings).

## Prior Documentation

- **Existing README.md:** none. Nothing renamed to `README.original.md`.
- **Existing LICENSE:** none; added `LICENSE` in this apply session.
- **Existing CHANGELOG:** none.
- **Session/state files present in the library root (excluded from library docs per BOOT_STANDARDIZATION rule 2):**
  - `/Users/chrispian/Projects-apps/framework/libs/go-mcp/CLAUDE.md` — session/state file, excluded from library docs.
  - `/Users/chrispian/Projects-apps/framework/libs/go-mcp/agentrc.yaml` — session/state file, excluded from library docs.
  - `/Users/chrispian/Projects-apps/framework/libs/go-mcp/.agentrc/` (contains `agent-boot.md`, `bootstrap.md`, `boot/`, `logs/`, `pcc/`, `tasks/`) — session/state files, excluded from library docs.
- **Other non-doc config files present:** `.golangci.yml`, `lefthook.yml`, `.git/` (normal tooling; not covered by BOOT_STANDARDIZATION rules but noted for completeness).
- **`docs/` subfolder:** not present.

## Public API Snapshot

Grouped by file. All public API is in package `budget` (import
`github.com/hollis-labs/mcp-helpers/budget`).

### `budget/envelope.go`
- Package doc: `// Package budget provides MCP response budget enforcement utilities.` (references ADR-006)
- Type `Envelope struct { Items any; Count int; Total int; Truncated bool; Hint string }` (JSON-tagged)

### `budget/budget.go`
- Const `DefaultLimit = 10`
- Const `MaxLimit = 25`
- Const `DefaultMaxTokens = 2000`
- Const `DefaultMaxBytes = 8000`
- Type `Config struct { Limit int; MaxBytes int; MaxTokens int }`
- Method `(Config).withDefaults() Config` (unexported)
- Func `Apply[T any](items []T, cfg Config, hintTemplate string) Envelope`

### `budget/helpers.go`
- Func `Clamp(v, min, max int) int`
- Func `ExtractLimit(params map[string]any, defaultVal int) int`
- Func `ExtractPagination(params map[string]any) (limit, offset int)`
- Func `ToolJSON(v any) string`
- Func `ToolError(code, message string) string`
- Func `extractInt(params map[string]any, key string, defaultVal, min, max int) int` (unexported)

### `budget/tokens.go`
- Func `EstimateTokens(payload []byte) int`
- Func `EstimateTokensFromString(s string) int`

## Open Questions

1. **What is the intended canonical module path?** `github.com/hollis-labs/mcp-helpers` vs. a framework-org path — resolving this affects every downstream import.
2. **Should `go-mcp` grow additional subpackages** (e.g., transport helpers, tool-schema builders), or is `budget` the entire intended surface? The directory name suggests the former.
3. **Is ADR-006 public and versioned anywhere in the framework repo?** The package doc treats it as canonical but does not link to it.
4. **Should `Config.MaxBytes` / `Config.MaxTokens` be enforced** or removed? Silent no-op is currently the behavior.
5. **Which license should be applied?** Must match sibling libs under `framework/libs/`.
6. **Is this module expected to be `go install`-able standalone**, or only consumed via a parent workspace / replace directive? That affects whether the module path mismatch is truly a blocker.
