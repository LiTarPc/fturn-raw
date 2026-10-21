# FturnRaw version numbers

Application, client core and server core have independent version numbers.
The settings panel reads the client core version from `runtime/raw-client.exe --version`.
It does not report the version installed on the remote server.

| Component | Previous saved release | New multi-device build |
| --- | --- | --- |
| Windows application / interface | 0.1.13 | 0.1.13 (interface retained) |
| Raw client core | 0.1.12 | 1.0.0 |
| Raw server core | 0.1.10 | 1.0.0 |

The previous application 0.1.13 intentionally kept the exact core from package
0.1.12. The server 0.1.10 had already enabled rtpopus2 and rtpopus3. Client
0.1.11 updated online CIDR refresh, and client 0.1.12 introduced the experimental
TCP flow uplink scheduler; neither required a server update. The old server
allowed one client process per key and replaced it when another connected.

Raw 1.0.0 adds multiple concurrent clients sharing one link, with a default limit
of three devices. The server must be upgraded to enable this behavior; a newer
client alone cannot add it to an old server. Existing client links remain usable.
Forwarding/NAT must cover all assigned server-side addresses; see raw-server.md.

Current client core version: `internal/buildinfo/core-version.txt`.
Current server core version: `internal/buildinfo/server-version.txt`.
Application version: `ui/newservice/wails.json` (`info.productVersion`).
Server and UI build scripts stamp the matching component version.

Check the exact binaries:

```powershell
.\runtime\raw-client.exe --version
# FturnRaw core 1.0.0
.\raw-server.exe --version
# FturnRaw server 1.0.0
```

Historical 0.1.12/0.1.13 client archives and 0.1.10 server archives keep their
original names and contents. New versioned output folders and suffixed packages
keep both generations available locally.
