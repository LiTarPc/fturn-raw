# Raw client 0.1.12: Linux and Android

These are clients for Raw server 0.1.10 and its existing single-client protocol.
They do not enable multiple devices sharing one server/key. The Windows UI is
version 0.1.13; the client transport version is independently 0.1.12.

## Linux amd64 / arm64

Extract the matching client archive, then `chmod +x raw-client`.
A TUN device, root/CAP_NET_ADMIN and the `ip` utility are required.

```sh
./raw-client --version
sudo ./raw-client -peer VPS_IP:56010 -links 'VK_CALL_URL' -obf-key-file raw.key
```

The key must match the Raw server. Default address: 10.77.0.2/24; MTU: 1280;
RTP profile: rtpopus2. Set the same MTU/profile as the server. Use `--help` for
all flags. `-state-dir` selects a persistent directory for the client identity.
The core creates its TUN and assigns the address but preserves the default route.
Test `ping 10.77.0.1` first. Full VPN routing, DNS, control-traffic exceptions,
forwarding/NAT and IPv6 policy must be supplied by the host integration; see
`docs/raw-experiment.md`. Do not redirect VK/TURN control traffic back into TUN.
The CLI takes explicit arguments; it does not import `fturnraw://` links.

## Android application (arm64-v8a, no root)

`fturn-raw-client-0.1.12-android-arm64-vpnservice.zip` contains
`arm64-v8a/libfturnraw.so`. This is a PIE executable launched by ProcessBuilder,
not a JNI library or an AAR. It matches the existing Fturn RAW Android app:

- Package it as `app/src/main/jniLibs/arm64-v8a/libfturnraw.so` and preserve Go
  symbols/sections with the existing `jniLibs.keepDebugSymbols` configuration.
- VpnService creates and configures the IPv4 TUN, MTU, routes and VPN DNS.
- Launch the core with the normal Raw CLI arguments and environment variable
  `FREETURN_RAW_TUN_SOCKET` naming the app's random abstract Unix socket.
- Send exactly one duplicate TUN descriptor via SCM_RIGHTS, with payload
  `10.77.0.2/24|1280` (or the selected address and MTU).
- The existing app checks the receiver UID. The core validates address/MTU and
  uses nonblocking I/O so Close interrupts a pending read.
- Exclude the app UID from its VPN so VK/TURN traffic remains outside the tunnel.

This archive does not contain an APK and does not change the legacy core
`libfreeturn.so`. Build the Android APK in its own repository after integrating
this executable. The existing application imports `fturnraw://` configurations.

## Android Termux CLI (arm64, root required)

`fturn-raw-client-0.1.12-android-arm64-termux.zip` contains `raw-client`.
This variant uses the `fturntermux` Go build tag to create/configure `/dev/net/tun`
directly. It does not expect a VpnService descriptor and is distinct from the
application executable. It requires root, kernel TUN support, access allowed by
SELinux and an `ip` utility accessible in the root process PATH. Non-root Termux
cannot create a device-wide VPN; use the Android application in that case.

Extract into Termux private storage (not /sdcard), then for example:

```sh
chmod 700 raw-client
./raw-client --version
# From a root shell in this directory, with ip available in PATH:
./raw-client -peer VPS_IP:56010 -links 'VK_CALL_URL' -obf-key-file raw.key
```

Routing is managed explicitly, as for the Linux client. The executable alone
does not install default VPN routes or Android DNS settings. This rooted-device
mode needs validation on the target rooted phone; SELinux policies vary.

## Rebuild from this source

PowerShell, Go 1.26.6 or newer:

```powershell
./scripts/build-platform-clients.ps1
```

The Android dependency github.com/wlynxg/anet v0.0.5 requires linker option
`-checklinkname=0`. Android executables use GOOS=android, GOARCH=arm64,
CGO_ENABLED=0, and buildmode=pie. Version is read from
`internal/buildinfo/core-version.txt`; it is not hard-coded to a test label.
The script writes binaries under `dist/platform-clients/`.
