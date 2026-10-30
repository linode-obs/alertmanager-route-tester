AI-generated content prepared on Will's behalf.

# Open issues implementation plan

Goal: Resolve upstream issues #34 through #43 in one worktree.

Architecture: Extend existing Go handlers, native route evaluation, and HTMX templates. Keep drafts in page memory and labels in session storage.

Technology: Go 1.27.1, Alertmanager 0.33.0, yaml.v3, HTMX, existing agent-browser installation.

## Global constraints

Use standard library application code except the already approved modules. Do not install tools or add dependencies.
Use `mise run build` for instrumented binaries and `mise run test` before each commit.
Run failing regression tests before each implementation. Keep all work on `wip/open-issues`.
Do not push, close issues, upload artifacts, or change live Alertmanager configuration.
Use Conventional Commit subjects with the required disclosure in the body.

## API contract, #34

Files: `internal/handler/handler_test.go`, `internal/handler/handler.go`, `README.md`.

- Write request contract tests for null labels, wrong label types, trailing data, unknown instances, body limits, and methods.
- Pin response fields using a literal nested route fixture and the real handler.
- Run `mise exec -- go test ./internal/handler -run 'Test.*JSON|Test.*Contract'` and observe missing validation.
- Reject invalid label objects without changing the existing response fields.
- Document the POST schema and curl usage. Run the full suite and commit.

## System fonts, #36

Files: `main_test.go`, `static/style.css`.

- Test that stylesheet assets do not refer to an external host.
- Run the test and observe the Google Fonts import failure.
- Remove the import and use the exact stacks specified in the issue.
- Run the test and full suite. Commit.

## Diagnostic limit, #39

Files: `internal/config/config.go`, `internal/config/config_test.go`, `internal/handler/handler.go`, `internal/handler/handler_test.go`, `main.go`, `config.yaml`, `README.md`.

- Test default 500, custom limit 2, negative rejection, and omitted counts from an actual rendered routing result.
- Run targeted tests and observe the fixed limit failure.
- Add `ServerConfig.RouteMismatchLimit`, default it, reject negative values, and pass it to the handler.
- Document `server.route_mismatch_limit`. Run the full suite and commit.

## YAML labels, #38

Files: `templates/index.html`, `internal/handler/labels.go`, `internal/handler/labels_test.go`, `main.go`, `scripts/test-browser.sh`.

- Add a browser regression that enters labels, switches modes, and asserts equivalent YAML.
- Test real YAML parsing of quoted values, Unicode, empty values, malformed mappings, and multiple documents.
- Observe failures before adding a bounded label-import handler and YAML serialization in the page.
- Use JSON string quoting for the emitted YAML subset. Keep invalid imports from replacing current labels.
- Run parser tests, browser tests, and the full suite. Commit.

## Reload preservation, #37

Files: `templates/index.html`, `scripts/test-browser.sh`.

- Test entered labels, unfinished fields, raw YAML, and editor mode across an actual reload.
- Observe loss of state before implementing session storage and restore scoped to the selected instance.
- Test the storage-unavailable confirmation path. Run full tests and commit.

## Label reuse and contextual suggestions, #40

Files: `internal/alertmanager/suggestions.go`, `internal/alertmanager/suggestions_test.go`, `internal/handler/labels.go`, `templates/index.html`, `scripts/test-browser.sh`.

- Test that a selected parent label excludes suggestions from incompatible paths.
- Test successful label sets in the recent selector, deduplication, limits, and clearing in a real browser.
- Observe failures before implementing native-matcher filtering and session-scoped history.
- Keep manual entry and current datalists. Run full tests and commit.

## Receiver explanations, #41

Files: `internal/alertmanager/receiver_paths.go`, `internal/alertmanager/receiver_paths_test.go`, `internal/handler/handler.go`, `templates/index.html`, `templates/result.html`.

- Test missing labels, wrong values, ancestor failures, inherited receivers, multiple paths, sibling blocking, and child delivery.
- Observe missing receiver-path diagnostics before implementation.
- Walk every native route path for the requested receiver. Report conditions and actual traversal blockers.
- Render an optional receiver selector and all relevant paths without truncation from the general mismatch limit.
- Run native tests, browser tests, and the full suite. Commit.

## Route-specific examples, #42

Files: `internal/alertmanager/client.go`, `internal/alertmanager/client_test.go`.

- Test more than three examples for distinct configured routes and explicit label and receiver descriptions.
- Use SaltStack labels in a fixture and assert an appropriate example without inventing unsupported labels.
- Observe the current small generic sample failure. Expand the bounded sample selection and domain names.
- Retain routing confirmation and generation limits. Run the full suite and commit.

## Draft configuration, #43

Files: `internal/alertmanager/client.go`, `internal/handler/draft.go`, `internal/handler/draft_test.go`, `internal/handler/handler.go`, `main.go`, `templates/index.html`, `templates/result.html`, `static/style.css`, `scripts/test-browser.sh`.

- Test baseline loading, valid and invalid draft parsing, unified diff output, request limits, selected matcher mode, and unchanged cached live routing.
- Observe missing draft behavior before implementing draft parse and test endpoints.
- Submit drafts per request. Reuse the current result renderer and mark draft results.
- Generate a bounded unified diff with the standard library. Never persist or log draft contents.
- Test editing, preview, routing, discard, and navigation warnings in a real browser.
- Run the full suite and commit.

## Route ladder, #35

Files: `static/style.css`, `scripts/test-browser.sh`.

- Test route ladder overflow and label readability at desktop and tablet widths in both themes.
- Observe current overflow before changing relevant wrapping, grid, and contrast rules.
- Capture local screenshots and inspect both themes. Run the full suite and commit.

## Final acceptance

- Run `mise run start` and the complete local browser regression script.
- Run `mise run test` and the configured static analysis commands that are already installed.
- Review the entire branch diff against every acceptance criterion.
- Make sure that the worktree is clean and every commit uses the requested format.
- Report completed issues, tests, branch, and remaining limitations without pushing.
