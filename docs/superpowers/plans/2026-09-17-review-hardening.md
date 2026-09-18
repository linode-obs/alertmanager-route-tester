# Review Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the verified repository defects selected from the full-repository review, improve CLI testability, and make packaged and local execution reliable without changing the unresolved authentication policy.

**Architecture:** Keep the routing engine and HTTP handlers in their existing packages. Add bounded request/response handling and request-context propagation at the client boundary, embed runtime assets, keep the default listener on loopback, and expose explicit CLI inputs for labels and Alertmanager selection. Preserve receiver data behavior for now because the Alertmanager redaction contract has not been independently confirmed in this session.

**Tech Stack:** Go 1.27.1, standard Go HTTP packages, `gopkg.in/yaml.v3`, Go templates, HTMX, mise, GoReleaser.

## Global Constraints

- Use TDD for behavior changes.
- Keep the standard-library-only instruction decision separate from the existing YAML dependency.
- Do not add authentication or authorization in this change.
- Do not modify or expose untracked `.ralph/` or `.superpowers/` files.
- Use conventional commit messages.
- Do not push or open a pull request.

---

### Task 1: Establish Go 1.27 toolchain

**Files:**
- Modify: `.tool-versions`
- Modify: `.mise.toml`
- Modify: `go.mod`

- [ ] **Step 1: Install Go 1.27.1**

Run `mise install go@1.27.1`.

- [ ] **Step 2: Update version declarations**

Set the project Go version to `1.27.1` in `.tool-versions`, keep the mise Go requirement aligned, and update the `go` directive in `go.mod` to `1.27`.

- [ ] **Step 3: Verify the toolchain**

Run `go version` and `go list -m` using the project toolchain.

- [ ] **Step 4: Commit**

```bash
git add .tool-versions .mise.toml go.mod
git commit -m "build: update Go toolchain"
```

### Task 2: Restore baseline tests and documentation

**Files:**
- Modify: `internal/handler/handler_test.go:652-666`
- Modify: `README.md:209`
- Modify: `.goreleaser.yaml:16-18`

- [ ] **Step 1: Fix the failing template fixture**

Add the fields required by `index.html` to `TestIndexTemplateIncludesShareLinkControls`.

- [ ] **Step 2: Fix the documentation link**

Use the tracked `AGENTS.md` filename.

- [ ] **Step 3: Remove unused linker flags**

Remove linker flags targeting undefined `main.Version`, `main.Commit`, and `main.BuildDate`; retain runtime VCS metadata from `buildInfo`.

- [ ] **Step 4: Run focused tests**

Run `go test -short ./internal/handler ./internal/config ./internal/cli ./internal/alertmanager`.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/handler_test.go README.md .goreleaser.yaml
git commit -m "fix: restore repository verification"
```

### Task 3: Embed runtime assets

**Files:**
- Modify: `internal/handler/handler.go`
- Modify: `main.go`
- Test: `internal/handler/handler_test.go`

- [ ] **Step 1: Write a failing asset-loading test**

Exercise handler construction and static asset serving from a working directory that is not the repository root.

- [ ] **Step 2: Run the focused test and verify failure**

Confirm the current relative-path implementation fails outside the repository root.

- [ ] **Step 3: Embed templates and static files**

Use `embed.FS` and `template.ParseFS` for templates. Use an embedded filesystem-backed static handler.

- [ ] **Step 4: Run focused and full tests**

Run handler tests, then `go test -short ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go main.go templates static
git commit -m "fix: embed web assets in the binary"
```

### Task 4: Bound HTTP payloads

**Files:**
- Modify: `internal/handler/handler.go`
- Modify: `internal/alertmanager/client.go`
- Test: `internal/handler/handler_test.go`
- Test: `internal/alertmanager/client_test.go`

- [ ] **Step 1: Write failing size-limit tests**

Test that oversized `/test` requests receive HTTP 413 and that oversized Alertmanager status/error bodies fail without unbounded reads.

- [ ] **Step 2: Run focused tests and verify failure**

Confirm the current handlers and client accept or read oversized bodies.

- [ ] **Step 3: Add limits**

Use a 1 MiB inbound request limit and a 10 MiB Alertmanager response limit. Return a clear client-size error and bounded upstream error.

- [ ] **Step 4: Run focused and full tests**

Run the package tests and `go test -race -short ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go internal/alertmanager/client.go internal/alertmanager/client_test.go
git commit -m "fix: bound HTTP payload sizes"
```

### Task 5: Propagate request cancellation and graceful shutdown

**Files:**
- Modify: `internal/alertmanager/client.go`
- Modify: `internal/handler/handler.go`
- Modify: `main.go`
- Test: `internal/alertmanager/client_test.go`
- Test: `internal/handler/handler_test.go`

- [ ] **Step 1: Write failing cancellation tests**

Verify a canceled request stops upstream work and retry backoff.

- [ ] **Step 2: Implement context-aware client operations**

Thread request contexts through handler fetches, use them when creating requests, and replace retry sleeps with context-aware timers.

- [ ] **Step 3: Add graceful server shutdown**

Handle SIGINT and SIGTERM, call `Shutdown` with a bounded timeout, and treat `http.ErrServerClosed` as normal.

- [ ] **Step 4: Run tests**

Run focused client and handler tests, then the race-enabled suite.

- [ ] **Step 5: Commit**

```bash
git add internal/alertmanager/client.go internal/handler/handler.go main.go internal/alertmanager/client_test.go internal/handler/handler_test.go
git commit -m "fix: cancel upstream requests during shutdown"
```

### Task 6: Improve CLI testability

**Files:**
- Modify: `main.go`
- Modify: `internal/cli/test.go`
- Test: `internal/cli/print_test.go`
- Test: `internal/cli/test_test.go`
- Modify: `README.md`

- [ ] **Step 1: Define CLI input behavior with tests**

Add tests for selecting an Alertmanager by name and supplying labels from a JSON input flag or stdin path, using real routing logic.

- [ ] **Step 2: Implement CLI selection and label input**

Add explicit flags while preserving config-file mode. Reject unknown instances and malformed label input with useful exit behavior.

- [ ] **Step 3: Expand simple output**

Include matcher and grouping/timing fields that already exist in JSON and web output.

- [ ] **Step 4: Update CLI documentation**

Document invocation examples for named Alertmanagers and per-run labels.

- [ ] **Step 5: Run tests**

Run all CLI tests and the full short suite.

- [ ] **Step 6: Commit**

```bash
git add main.go internal/cli README.md
git commit -m "feat: improve CLI routing test inputs"
```

### Task 7: Review and update repository documentation

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md` only if the dependency policy is explicitly resolved

- [ ] **Step 1: Audit documented commands and runtime behavior**

Align build, test, startup, embedded assets, CLI, listener address, and input-format documentation with the implementation.

- [ ] **Step 2: Run documentation consistency checks**

Verify referenced files exist and documented commands match the task definitions.

- [ ] **Step 3: Commit**

```bash
git add README.md AGENTS.md
git commit -m "docs: align usage with runtime behavior"
```

### Task 8: Final verification

**Files:**
- No planned source changes.

- [ ] **Step 1: Run formatting and static checks**

Run `gofmt -l`, `go vet ./...`, and the configured lint command if available without installing additional tools.

- [ ] **Step 2: Run tests**

Run `go test -race -short ./...` and `go test ./...` where integration dependencies are available.

- [ ] **Step 3: Verify release behavior**

Build from the worktree and run the binary from a separate working directory to confirm embedded templates and static files work without repository-relative assets or network access for HTMX.

- [ ] **Step 4: Inspect git state**

Confirm only intended tracked files changed and no untracked session artifacts were added.
