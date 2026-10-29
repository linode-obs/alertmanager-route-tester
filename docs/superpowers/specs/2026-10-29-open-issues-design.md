AI-generated content prepared on Will's behalf.

# Open issue design

Resolve upstream issues #34 through #43 in one Herdr worktree on `wip/open-issues`.
Create separate Conventional Commits for independently tested changes. Do not push or close issues.

## Approach

Keep the Go server, HTMX templates, native Alertmanager matcher, and existing visual design.
Do not add application dependencies. Use the existing YAML parser and Alertmanager modules.

Three approaches exist: extend the current application, replace the frontend, or build a separate draft service.
Extend the current application because the other approaches add deployment and maintenance work without meeting additional acceptance criteria.

## JSON API, #34

Document `POST /test`, its request fields, response fields, status codes, and one curl example.
Keep POST as the only route-test method. GET exposes alert labels in request URLs and adds no required behavior.
Pin successful responses and invalid JSON, oversized bodies, unknown instances, and unsupported methods with behavioral tests.
Keep existing response fields and nested route metadata.

## Route ladder, #35

Keep the route ladder and existing theme tokens.
Increase chip and heading contrast. Prevent label names and short operator values from breaking inside words.
Allow long values to wrap without forcing the page wider than the viewport.
Reflow matched-label rows on narrow screens. Test desktop and tablet widths in both themes.
Do not redesign unrelated sections.

## Fonts, #36

Remove Google Fonts requests and use the exact system font stacks in the issue.
Add a test that rejects external stylesheet resources.

## Reload preservation, #37

Keep alert labels, editor mode, raw input, and unfinished label fields in browser session storage before configuration reloads.
Restore that state after the page refreshes. Scope saved state to the selected Alertmanager.
If storage fails, request confirmation before discarding entered values.
Do not send saved label state to another Alertmanager.

## YAML mode, #38

Populate the raw editor from current labels when the user enters YAML mode.
Quote names and values so colons, quotes, backslashes, newlines, Unicode, and empty values remain strings.
Use the server's YAML parser for label imports if the current browser parser cannot preserve those values.
Reject malformed input without replacing the current labels.

## Diagnostic limit, #39

Add `alertmanager-route-tester.server.route_mismatch_limit` with a default of 500.
Reject negative values. Treat an omitted or zero value as the default.
Pass the effective limit into the handler. Keep omitted-diagnostic counts accurate.
Document the field in the README and example configuration.
If Helm values expose the field, update the chart version in the same commit.

## Label reuse and suggestions, #40

Add a recent-label selector for successful route tests in the current browser tab.
Keep at most ten distinct label sets per selected Alertmanager in session storage.
Provide an explicit clear action. Do not store receiver credentials or complete Alertmanager configurations.

Keep manual label entry available. Offer names and concrete values from relevant configured route paths.
Narrow suggestions using labels already entered. Do not claim that arbitrary regex text is a concrete matching value.
Use the existing native matcher to evaluate route constraints.

## Receiver explanations, #41

Add an optional configured-receiver selector next to the routing action.
Show every route path that resolves to the selected receiver, including inherited receiver names.
Explain missing labels, mismatched values, failed ancestor conditions, and earlier sibling routes that stop traversal.
Distinguish a matched parent whose child receives the alert from an effective receiver delivery.
Use native matcher semantics for all operators and absent labels.
Do not hide relevant receiver paths behind the general mismatch limit.

## Quick examples, #42

Generate a broader bounded selection of distinct route scenarios instead of two generic matching alerts.
Use route labels and receiver names in titles and descriptions.
Include continued delivery, nested routes, and root fallback when the selected configuration permits them.
Include SaltStack scenarios only when configured labels support them.
Confirm each displayed receiver outcome with the routing engine.
Keep the existing cancellation, candidate, and large-tree limits.

## Configuration drafts, #43

Will selected browser-tab drafts. Do not persist configuration drafts in browser storage or shared server state.
Keep fetched baseline text and proposed draft text separate in the current page.
Show an editable textarea and a unified diff with fetched and draft filenames.
Use bounded request bodies and reject invalid drafts without changing the baseline or cached configuration.

Submit the draft with each draft routing test. Parse it with the selected instance's matcher mode.
Reuse the native route matcher and existing result rendering.
Mark results as draft results. Never write configuration to Alertmanager or contact draft receiver endpoints.
Discard clears the draft and returns routing tests to the fetched configuration.
Warn before reloads or instance changes discard an edited draft.
Do not include drafts or receiver secrets in share links, logs, metrics, or telemetry attributes.

## Tests and delivery

For each change, write a failing behavioral test and run it before changing application code.
Use real native routing logic and local Alertmanager for integration tests. Do not mock browser end-to-end behavior.
Run `mise run test` before each commit. It builds the instrumented binary through `otelc`.
Run `mise run start` for local browser testing after implementation.
Capture screenshots locally. Do not upload screenshots, source, logs, or configuration.

The baseline `mise run test` passed on 2026-10-29 with Alertmanager 0.33.0 and Go 1.27.1.
The worktree reuses the already installed Alertmanager binary through an ignored symlink.
