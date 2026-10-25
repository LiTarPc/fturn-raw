# FturnRaw Windows UI 0.1.14 beta

Windows UI 0.1.14 with the previously released **client core 1.0.1-beta**. This release publishes the Windows UI audit fixes separately from the core release. Linux/Android clients and server packages remain available in [core v1.0.1-beta](https://github.com/LiTarPc/fturn-raw/releases/tag/v1.0.1-beta).

## Fixed
- Late polling/command/event snapshots cannot replace newer connection state. Backend snapshots carry a monotonic revision; polling is serialized.
- Disconnect stays available during RU CIDR downloads. Duplicate connection commands are suppressed and overlapping operations keep correct busy state.
- Logs show the selected TCP/UDP transport, with idle transport explicitly marked as a profile setting.
- Retry output from an old core process cannot remove a ready stream belonging to a new session.
- A stdout scanner error terminates the affected child before Wait and preserves the original read error.
- A second launch signals the first process to restore its window, including launches during startup, while retaining the single-instance mutex.
- ui.log rotates at 5 MiB with three backups; write failures are shown in the UI.

## Packages and installation
- setup.exe: Windows x64 installer.
- portable.zip: extract the complete archive and run FturnRaw.exe with runtime/ alongside it.
- SHA256SUMS: SHA-256 hashes of both packages.

Exit the previous UI through Exit before starting the updated UI. Version 0.1.13 does not receive the new window-restore signal. Existing user profiles/settings are preserved by the installer. Packages remain unsigned.

This is a prerelease and is not marked Latest. Install it manually from this release; it does not replace the stable update channel.

## Validation
The release is gated on 10 React regression tests, ESLint, TypeScript/Vite build, Windows backend/process tests, core protocol tests, route/watch regression checks, native DNS firewall checks, installer install/upgrade/uninstall checks, and package SHA-256 validation. Tests include real Windows child processes for oversized stdout and second-instance signaling.

Window restoration policy is tested through WindowHost; React tests run in jsdom. Full WebView2/DPI testing, real VK/VPS/TUN/NAT traffic and a long soak test are outside this release's validation scope.
