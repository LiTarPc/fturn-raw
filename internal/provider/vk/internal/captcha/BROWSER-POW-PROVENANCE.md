# Browser PoW integration provenance

Based on hackdiaz-dev/free-turn-proxy tag v4.1.0, pinned commit
465486ba96c4c2ff93443682ec900510b2e0ff9e (GPL-3.0; project LICENSE applies).
Repository: https://github.com/hackdiaz-dev/free-turn-proxy

Ported the browser capture assignment hook, browser-envelope validation,
UA/client-hint alignment and SolveWithBrowserPow selection. Adapted the callbacks
to this RAW CLI's authenticated IPC and existing provider retry controls.

Intentional differences from upstream:
- Advanced is opt-in, not a default change for existing clients.
- Capture is bounded to 12 seconds and may not submit provider verification;
  result/token channels are separate, generation/request_id/TTL remain enforced.
- No raw PoW/telemetry, request bodies, headers or cookies in diagnostics.
- Invalid proof is not submitted; no repeated reuse of a browser envelope.
- One automatic attempt then one fresh manual challenge, no identity/client_id
  rotation on solve failure, process-wide BOT gate across advanced VK links.
- HTTP/API rate limits and Retry-After, VK service error handling, active caches,
  UDP lifecycle cleanup, health and TURN maintenance are retained from android.5.

Offline fixture success is not evidence of acceptance by VK. Android WebView E2E
and a controlled natural provider challenge are separate acceptance steps.
