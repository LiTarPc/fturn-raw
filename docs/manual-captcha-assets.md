# Manual CAPTCHA resource paths

A manual CAPTCHA can open as an empty page when its entry script is loaded through
`/generic_proxy?proxy_url=...`. The upstream Webpack loader derives its public
directory from `document.currentScript.src`, stripping the query and entry filename.
That loses the original CDN directory: a request for a lazy JavaScript chunk goes
to `id.vk.ru/chunks/js/...` instead of the project directory on `st.vk.ru`.
An HTML 404 response then appears as a JavaScript MIME error. Relative CSS and
font paths have the same problem.

External scripts, styles and API requests now use
`/local-captcha-resource/<base64url-origin>/<original-path>?query`.
The browser-visible directory retains the original path, so Webpack chunks and
relative CSS/font URLs resolve to the same upstream location. The legacy
`/generic_proxy` route shares the new handler for compatibility.

## Scope and preserved behavior

This change is based on `codex/core-1.1.0` at
`11265eb059a6894e2c57109602badcf91660380f`.
Core/server version 1.1.0, Go dependencies, cancellation/goodbye handling and
stream limits are unchanged. No release or tag is created by this branch.

- Both proxy routes retain the existing upstream host allowlist.
  Unsupported schemes and URL credentials are rejected; the resource origin
  must contain only a scheme and authority. The separate `ad.mail.ru` advertising
  request stays forbidden.
- Upstream escaped paths, query strings, HTTP status and Content-Type are
  preserved. A 404 HTML response remains a 404 HTML response.
  `X-Content-Type-Options: nosniff` is retained.
- Protocol-relative URLs use the upstream scheme, and rewriting
  `fetch(Request)` preserves its method, body and headers.
- RAW's existing `captureReadOnlyTransport` permits GET/HEAD and rejects other
  methods. The new resource route goes through this same transport.
  This branch does not add a GET endpoint denylist.
- Success-token response inspection uses the upstream path. A marker in a
  resource query cannot turn its response into a verification result.

## Verification

The Node regression can run against the current injector or an explicitly
supplied baseline injector:

```text
node internal/provider/vk/internal/captcha/manual/resource_paths_test.mjs [injector-file]
node internal/provider/vk/internal/captcha/manual/inject_fixture_test.cjs
go test -mod=readonly ./internal/provider/vk/... ./internal/captchaipc -count=1
python scripts/verify-core-source.py
```

The unpatched injector fails the regression at the wrong upstream chunk URL;
the patched injector passes. Go tests cover Webpack/CDN directory resolution,
CSS fonts, query parameters, escaped paths, POST body/headers, status/MIME,
untrusted origins, the actual capture transport through both routes, and
resource-query success-token confusion. The existing offline proof-capture
fixture remains part of the checks.

Fresh VK widget rendering and successful manual completion still require a
live session. Offline fixtures and public CDN availability do not establish
that acceptance result.
