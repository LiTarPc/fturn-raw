# Raw reconnect and TURN maintenance fix — 2026-10-02

The supplied 51-minute log contains 34 TCP resets to VK TURN. 31 occurred within 30 seconds of a multiple of five minutes since that worker last became ready. The old server kept selecting failed workers until its 90-second liveness cutoff; a reconnect added a new relay without immediately retiring the old one.

## Changes

* A 55-byte hello extends the legacy 39-byte hello with a stable random worker identity. Client handshake nonce remains freshly random on every reconnect. The worker identity is included in session key derivation; legacy hellos remain supported.
* On authenticated confirmation, a new session retires the old session of the same process group and logical worker. Pending/unconfirmed sessions do not retire existing workers. Replacing a worker is allowed at the 64-worker limit.
* A failed worker queues an encrypted retirement notice for another working path. It identifies the logical worker and the old server session nonce. Delayed or repeated notices cannot remove a replacement or a different process group.
* Raw alone enables staggered ChannelBind refresh at 110–130 seconds with a five-second check, and allocation refresh at 225–255 seconds capped at the upstream initial half-lifetime. The legacy relay keeps its upstream defaults.
* Pion TURN v5.0.12 is pinned under third_party/turn with a small documented patch; the global module cache is unchanged. Non-438 error responses to Refresh are reported as errors instead of accidentally returning nil. Raw recycles after an allocation-refresh failure.
* Selected maintenance diagnostics are visible at INFO without promoting credentials/nonces/raw STUN frames.

## Validation

All parent-module tests and targeted go vet passed. Local real TURN/TCP tests inject a TCP reset and verify all 48 post-recovery downlink packets are delivered. Tests cover stable worker replacement, fresh session keys on relay-address reuse, retirement boundaries, replay protection, and replacement at the 64-worker cap. Modified TURN unit tests and IPv4 E2E tests pass. Its IPv6 E2E times out both on the patched copy and the unchanged upstream module in this Windows environment. Race testing requires a C compiler and was not run.

Windows amd64, Linux amd64 and Linux arm64 binaries built. The Debian Raw service is updated with a rollback binary, leaving other VPN services in place. Personal Windows UI package uses the Newservice appearance and retains the original server/key/profile. Frontend build and backend tests pass; npm audit reports zero vulnerabilities.

A live in-memory probe was attempted without changing Windows routes. TCP to TURN is currently unavailable over the existing system VPN; it was left enabled. A separate VPS probe reached VK authentication but its join-page/CAPTCHA requests timed out. It was stopped so it would not compete with the user's manual test. No live loss-rate improvement is claimed yet. Test games for at least 15 minutes and inspect ui.log for Refresh/ChannelBind, retrying, and recovery timestamps.
