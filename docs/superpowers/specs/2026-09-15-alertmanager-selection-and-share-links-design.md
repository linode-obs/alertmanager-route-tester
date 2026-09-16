# Alertmanager Selection and Share Links

## Scope

Implement the two open issues without adding server-side state or a compatibility layer for the old singular configuration.

## Configuration and selection

The application accepts a named map under `alertmanagers`:

```yaml
alertmanagers:
  production:
    url: https://alerts.example.com
  staging:
    url: https://alerts-staging.example.com
```

The singular `alertmanager` configuration is rejected. Each named entry carries the existing HTTP, TLS, retry, and pool settings, with the same defaults applied independently. The application creates one client per named entry and selects an entry by name.

A single configured entry keeps the existing page behavior. With multiple entries, the page renders a selector. The selected name travels through page navigation, route tests, and config reloads using the `alertmanager` query/form value. Invalid or missing names produce an actionable error instead of silently testing the wrong cluster.

## Share links

The browser stores the current label map in a URL fragment. The fragment contains only versioned label data, never an Alertmanager URL or selected instance. The server therefore receives no shared label state and no endpoint is required.

On page load, the browser validates and decodes the fragment. Valid string-to-string label maps populate the existing label editor. Invalid, unsupported, or malformed fragments show a clear message and do not replace the current labels.

A share action encodes the current labels, copies the resulting URL, and reports success or clipboard failure. Raw YAML input and the label editor both continue to update the same current label map, so either input path can produce a share link.

## Testing

Configuration tests cover named map loading, required names and URLs, and rejection of the singular form. Handler tests cover selected-client routing and invalid selection handling. Browser verification covers UI and raw YAML labels, copied fragment URLs, page reload restoration, and invalid fragment messages.
