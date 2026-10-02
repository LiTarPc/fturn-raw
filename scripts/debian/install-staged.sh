#!/bin/sh
set -eu
# Run from an uploaded staging directory on the inspected Debian VPS.
[ "$(id -u)" = 0 ] || { echo 'Run as root' >&2; exit 1; }
[ "$(uname -m)" = x86_64 ] || { echo 'This staged binary is for x86_64' >&2; exit 1; }
[ -c /dev/net/tun ]
command -v ip >/dev/null
command -v iptables >/dev/null
command -v systemctl >/dev/null
[ "$(cat /proc/sys/net/ipv4/ip_forward)" = 1 ] || { echo 'IPv4 forwarding is required' >&2; exit 1; }
[ ! -e /etc/systemd/system/fturn-raw.service ] || { echo 'Raw service already exists; inspect before updating' >&2; exit 1; }
[ ! -e /etc/fturn-raw/raw.key ] || { echo 'Raw key already exists; refusing to replace it' >&2; exit 1; }
[ ! -e /sys/class/net/ftraw0 ] || { echo 'ftraw0 already exists' >&2; exit 1; }
if ip -4 route show | grep -q '^10\.77\.'; then echo '10.77 network is already routed' >&2; exit 1; fi
if ss -H -lun | grep -q ':56010 '; then echo 'UDP 56010 is in use' >&2; exit 1; fi
install -d -m 0755 /opt/fturn-raw
install -d -m 0700 /etc/fturn-raw
install -m 0755 raw-server /opt/fturn-raw/raw-server
install -m 0755 raw-firewall.sh /opt/fturn-raw/raw-firewall
umask 077
/opt/fturn-raw/raw-server -gen-obf-key > /etc/fturn-raw/raw.key
chmod 0600 /etc/fturn-raw/raw.key
install -m 0644 fturn-raw.service /etc/systemd/system/fturn-raw.service
systemd-analyze verify /etc/systemd/system/fturn-raw.service
systemctl daemon-reload
systemctl enable --now fturn-raw.service
attempt=0
until ip link show dev ftraw0 >/dev/null 2>&1 && ss -H -lun | grep -q ':56010 '; do
 attempt=$((attempt + 1))
 [ "$attempt" -lt 10 ] || { journalctl -u fturn-raw -n 20 --no-pager; exit 1; }
 sleep 1
done
systemctl is-active --quiet fturn-raw.service
ip -4 addr show dev ftraw0
ss -lunp | grep ':56010 '
echo 'Raw installed. Retrieve /etc/fturn-raw/raw.key over SSH; never print it to logs.'
