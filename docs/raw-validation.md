# Raw experiment validation — 2026-10-01

Branch: `experiment/raw-ip`, based on the local fturn-core 4.1.3 archive.
No remote repository was configured or pushed.

## Completed

- `go test ./...` — passed for all packages on Windows/386, Go 1.26.6.
- `go vet ./...` — passed.
- `GOARCH=amd64 go test ./internal/rawvpn ./internal/session` — passed.
- New tests used real Pion TURN allocations on loopback, both UDP and TCP,
  three parallel workers, bidirectional IPv4 transport up to MTU 1280.
- Authentication, wrong keys, direction separation, changed session challenges,
  duplicate/stale counters, header tampering, IPv4 length checks, source IP
  spoofing, lost handshake replies, process replacement and cancellation tested.
- Cross-compilation succeeded for Windows/amd64, Linux/amd64 and Linux/arm64.
- Windows x64 binaries executed with `-help` successfully; this does not load
  Wintun or change any routes/interfaces.
- Official Wintun 0.14.1 ZIP verified against its published SHA256:
  `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`.

## Local microbenchmark

`GOARCH=amd64 go test -run '^$' -bench BenchmarkEncryptedRawRoundTrip -benchmem ./internal/rawvpn`

Intel Xeon E5-2695 v3, Windows x64 test binary: 2738 ns/op, 467.55 MB/s,
0 B/op, 0 allocs/op at MTU 1280. This measures the codec only, including
one encryption and one authenticated decryption/replay check. It excludes
TUN, TURN, sockets, NAT and the network. It is not an internet speed result
and not a measured improvement over WG/DTLS.

## Live VPS and Windows validation

- Debian 13.7 / x86_64: separate `fturn-raw.service`, UDP 56010,
  Linux TUN `ftraw0` at 10.77.0.1/24, MTU 1280.
- Windows x64: real Wintun `ftraw0`, 10.77.0.2/24, elevated client;
  three authenticated VK TURN allocations using TCP to TURN.
- `TestLiveRawVK` passed using the supplied VK link and real server:
  ICMP replies through the server kernel TUN at IPv4 sizes 64 and 1280,
  plus an internet ICMP reply from 1.1.1.1 through the scoped NAT rule.
  This opt-in test uses a memory client device and does not change host routes.
- The separate real Windows TUN check passed: 3/3 internet ICMP replies,
  99..103 ms (100 ms mean), with a temporary 1.1.1.1/32 TUN route.
  That test route was removed afterward. Existing default routes were preserved.
- VPS firewall/NAT counters confirmed the test traffic. Existing VPS VPN
  services and the existing active Windows sing-tun adapter remained active.
- One short TCP throughput check to 10.77.0.1:5202 over real Windows TUN:
  download 10,747,904 bytes / 17.889 s = 4.81 Mbit/s;
  upload 1,769,472 bytes / 16.561 s = 0.85 Mbit/s.
  A small custom Python/PowerShell transfer ran roughly 15 seconds per direction;
  measured time includes delivery/draining. Three TURN streams, one inner TCP flow.
  This is a first measurement, not an iperf3 result, a speed ceiling or evidence
  of improvement over WG. No controlled WG baseline was measured.
- Temporary throughput service exited successfully and its TCP listener closed.
- `go vet ./internal/session` and regular session tests passed with the new
  opt-in live test; it skips when its three environment variables are unset.
- Private deployment files/keys are under ignored `dist/deployment-windows`;
  public build ZIPs and Git history do not contain the generated shared key.
## Not verified here

- Full default-route VPN, IPv6 handling and a controlled WG speed comparison.
- Android/iOS integration; this prototype is a separate Windows/Linux CLI.
- Race detector: `GOARCH=amd64 CGO_ENABLED=0 go test -race ./internal/rawvpn`
  could not run (`-race requires cgo`). No C compiler was available in PATH
  or the checked standard LLVM/MinGW locations. Run with a matching C toolchain
  in CI before treating this experiment as a production transport.
- Independent review/audit of the experimental protocol.

## MTU update and BrowserLeaks - 2026-10-01

- Raised supported inner MTU ceiling from 1350 to 1500; default CLI MTU stays 1280.
- New authenticated Raw fragment frames keep the outer IPv4/UDP size at most
  1431 bytes for rtpopus2. Maximum 64 incomplete assemblies per worker/session,
  3-second lifetime, source/destination validation after complete reassembly.
- Tested rtpopus and rtpopus2 round trips at IP sizes 1350, 1351, 1492, 1500,
  reordered pieces, malformed offsets/lengths, duplicates/replay, limits/expiry
  and session separation. Real local TURN TCP/UDP tests reached MTU 1500.
- All-package tests and vet passed. Windows x64/Linux x64/Linux ARM64 rebuilt.
- Live MTU 1500 passed to the VPS TUN. The full live test failed on internet
  ICMP size 1500. Direct VPS DF pings to 1.1.1.1 and 8.8.8.8 received an ICMP
  fragmentation-needed response from its gateway reporting MTU 1472.
- Selected MTU 1420 on the deployed server and Windows client. The opt-in live
  test passed to both VPS TUN and 1.1.1.1 at sizes 64 and 1420.
- Real Windows TUN: 3/3 DF pings with 1392 payload bytes (IPv4 total 1420) passed.
- BrowserLeaks request: Windows curl, no proxy, bound to 10.77.0.2, temporary
  host route to the resolved site IPv4. It reported MTU 1420, MSS 1380,
  Link Type generic tunnel or VPN; Zardaxt Android 37%, Linux 32%, Windows 28%,
  macOS 6%, iOS 5%. These are probabilistic scores for that request, not
  proof of Android emulation or a guarantee for a separate browser session.
- Temporary site route removed. The running client remains on MTU 1420.
- The old speed measurement above used MTU 1280 and was not repeated at 1420.
