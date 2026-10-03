# Raw server packages

Packages are published at https://github.com/LiTarPc/fturn-raw/releases:

- `fturn-raw-server-<version>-linux-amd64.zip`: Linux x86-64.
- `fturn-raw-server-<version>-linux-arm64.zip`: Linux ARM64 / aarch64.
- `fturn-raw-server-<version>-windows-amd64.zip`: Windows x64, including official Wintun 0.14.1.

Every archive contains the server binary, this guide, the core license and dependency notices. No server keys, VK links or personal configurations are bundled. Verify the archive against `SERVER-SHA256SUMS` in the release.

## Linux

Extract the archive matching the server CPU, then mark the binary executable (ZIP extraction may not preserve executable permissions):

```bash
chmod +x raw-server
umask 077
./raw-server -gen-obf-key > raw.key
sudo ./raw-server -listen 0.0.0.0:56010 -obf-key-file ./raw.key -mtu 1420
```

Linux requires `/dev/net/tun`, `iproute2`, and root or equivalent TUN/network privileges. For internet access, configure IPv4 forwarding, NAT for the Raw client, and allow UDP port 56010 from TURN relays. The server does not configure forwarding or NAT automatically. Deployment templates are in the repository's `scripts/debian`; the existing staged install helper targets Linux x64. For ARM64 use the same server flags with forwarding/NAT configured for your host.

## Windows x64

Extract the entire Windows archive. Keep `wintun.dll` next to `raw-server.exe`. Open PowerShell as administrator in this directory:

```powershell
.\raw-server.exe -gen-obf-key | Set-Content -LiteralPath .\raw.key -Encoding ascii
.\raw-server.exe -listen 0.0.0.0:56010 -obf-key-file .\raw.key -mtu 1420
```

Configure Windows IPv4 forwarding, routing/NAT and the UDP inbound firewall rule separately for the host's network. The Windows archive is a CLI core, without an installer or automatic service configuration. Its help/key-generation paths can run without creating a TUN adapter; a live server requires administrator privileges.

## Client configuration

Configure the client with the server's public IPv4 and port 56010, the same generated key, MTU 1420, and your VK call link. The server-facing port is UDP; the Windows client's connection to VK TURN uses TCP. Protect `raw.key` and configuration links containing it.

Defaults are server address `10.77.0.1/24`, client `10.77.0.2`, TUN `ftraw0`. One physical client is supported per server/key instance. A second computer using the same instance can replace the first client's session. Stop with Ctrl+C.

Source: https://github.com/LiTarPc/fturn-raw

## Build / validate

```powershell
.\scripts\build-raw-server.ps1 -Version 0.1.1
.\scripts\test-raw-server-packages.ps1
```

The build uses `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false` and explicit file lists. Architecture checks validate PE/ELF headers and package hashes; CLI `-help` is run only for a package native to the test host and does not touch networking. GitHub Actions tests server packages on Linux amd64, Linux arm64 and Windows x64 hosts. These checks do not validate a host's NAT or a live VK connection.
