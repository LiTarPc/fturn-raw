# Raw server packages

## Raw version lines

- **1.0.0**: multiple simultaneous devices sharing one configuration link; default limit is three (`-max-devices 3`).
- **0.1.12**: the previous Raw client core bundled with Windows application **0.1.13**.
- **0.1.10 (legacy server)**: the last previous server package; it stayed on this version while the client advanced to 0.1.12. Connecting another client process with the same key replaces the previous process's sessions.

Use `raw-server --version` to identify a versioned build. Server version 1.0.0 is stored in `internal/buildinfo/server-version.txt` and is independent of the Windows application/client version. The server build and package checks read this file by default. Versioned build folders prevent replacing the legacy server output. Historical published 0.1.x archives retain their original names and contents; some older binaries do not implement `--version`.

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

## RTP profile and client transport

New servers default to `-obf-profile auto` and accept both `rtpopus2` and `rtpopus3` on the same endpoint, after key authentication. You may explicitly restrict a server to either profile. Existing services with `-obf-profile rtpopus2` must be updated to `auto` before clients can select `rtpopus3`. Legacy `rtpopus` remains an explicit CLI-only option.

Both profiles use ChaCha20-Poly1305. rtpopus3 changes the RTP extensions and header behaviour; it adds 4 bytes per encrypted datagram. It is not standard SRTP or a guarantee of indistinguishability from a real call.

Windows profiles store `Transport` (`tcp`/`udp`) and `ObfProfile` (`rtpopus2`/`rtpopus3`). Missing values retain TCP + rtpopus2. New combinations use version 3 configuration links; older default links remain compatible. A transport change requires reconnecting. UDP availability depends on the selected underlying connection and the TURN service.

## Client configuration

Configure the client with the server's public IPv4 and port 56010, the same generated key, MTU 1420, and your VK call link. The server-facing port is UDP. The Windows client can use TCP or UDP to TURN, independently of the RTP profile. Protect `raw.key` and configuration links containing it.

Defaults are server address `10.77.0.1/24`, shared client address `10.77.0.2`, TUN `ftraw0`, and **three simultaneous devices per server/key**. Import the same configuration link on up to three devices; all may keep the same client IP, key, MTU and TURN settings. Stop with Ctrl+C.

Set `-max-devices 3` explicitly to keep this limit, or choose `1..64`. The fourth device is rejected without disconnecting existing devices. Updated clients report a device-limit error; older clients remain compatible but may report a handshake timeout when the server is full. Each device retains its own allowance of up to 64 TURN workers; workers do not count as separate devices.

A device slot represents one running client process, identified by its authenticated session group. This is a concurrent connection limit, not a permanent hardware registration. A clean disconnect releases its slot when its last worker closes. After a crash or lost connection, stale workers expire after 90 seconds, with cleanup every 10 seconds. A quick application restart after a crash can temporarily occupy another slot or be rejected until cleanup. Reconnecting a TURN worker within the same process keeps the device slot and only retires that worker's previous session.

The server translates the shared client IP into a distinct server-side address per device: with defaults, `10.77.0.2`, `10.77.0.3`, and `10.77.0.4`. Replies return only to the matching device. TCP/UDP checksums, IP fragments and ICMP error quotes are translated as well. The full address pool must fit inside `-address`, excluding the server, network and broadcast addresses. Reserve these addresses for Raw clients.

**Existing installations must extend forwarding/NAT rules beyond `10.77.0.2/32`.** A server-only binary update enables shared links, but rules allowing only the first address will block internet access for the other devices. For the default three-device pool on Linux, replace `eth0` below with your external interface:

```bash
sudo sysctl -w net.ipv4.ip_forward=1
for client_ip in 10.77.0.2 10.77.0.3 10.77.0.4; do
  sudo iptables -A FORWARD -i ftraw0 -o eth0 -s "$client_ip/32" -j ACCEPT
  sudo iptables -A FORWARD -i eth0 -o ftraw0 -d "$client_ip/32" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
  sudo iptables -t nat -A POSTROUTING -s "$client_ip/32" -o eth0 -j MASQUERADE
done
```

The `scripts/debian` service/firewall templates cover the same three addresses explicitly. If changing `-client-ip`, `-address`, or `-max-devices`, update your firewall/NAT rules to match the resulting pool. On an existing template-based installation, stop the service with its old firewall helper first, then replace the binary, helper and service file and start it again. Keep the existing key. On Windows, extend your host's routing/NAT configuration to cover the entire pool too.

Version history and separate application/client/server numbers: [raw-versions.md](raw-versions.md).

Source: https://github.com/LiTarPc/fturn-raw

## Build / validate

```powershell
.\scripts\build-raw-server.ps1 -Version 1.0.0
.\scripts\test-raw-server-packages.ps1
```

The build uses `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false` and explicit file lists. Architecture checks validate PE/ELF headers and package hashes; CLI `-help` is run only for a package native to the test host and does not touch networking. GitHub Actions tests server packages on Linux amd64, Linux arm64 and Windows x64 hosts. These checks do not validate a host's NAT or a live VK connection.
