# FturnRaw 0.1.13 component versions

| Component | Version |
| --- | --- |
| Windows application / interface | 0.1.13 |
| Raw client core | 0.1.12 |
| Raw server core | 0.1.10 |

The interface uses the burgundy palette and reports both application and actual
client-core versions. This release retains the exact client core from 0.1.12.
The separately published 0.1.10 server supports one client process per key;
connecting another process replaces the previous client's sessions.
The settings card reads `runtime/raw-client.exe --version`; server version is
checked separately with `raw-server --version` when supported by that build.

Current component versions are stored in `internal/buildinfo/core-version.txt`,
`internal/buildinfo/server-version.txt`, and `ui/newservice/wails.json`.
This release does not include the unpublished multi-device work.
