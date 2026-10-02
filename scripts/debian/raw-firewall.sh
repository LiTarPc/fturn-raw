#!/bin/sh
set -eu
# Only exact rules tagged fturn-raw-experiment are added/removed.
rule() {
 table=$1; chain=$2; shift 2
 if [ "$mode" = up ]; then
  iptables -w -t "$table" -C "$chain" "$@" 2>/dev/null || iptables -w -t "$table" -I "$chain" 1 "$@"
 else
  if iptables -w -t "$table" -C "$chain" "$@" 2>/dev/null; then
   iptables -w -t "$table" -D "$chain" "$@"
  fi
 fi
}
mode=${1:-}
case "$mode" in up|down) ;; *) echo 'Usage: raw-firewall up|down' >&2; exit 2;; esac
if [ "$mode" = up ]; then
 [ "$(cat /proc/sys/net/ipv4/ip_forward)" = 1 ] || { echo 'Enable IPv4 forwarding before starting Raw' >&2; exit 1; }
fi
rule filter INPUT -p udp --dport 56010 -m comment --comment fturn-raw-experiment -j ACCEPT
rule filter INPUT -i ftraw0 -s 10.77.0.2/32 -d 10.77.0.1/32 -m comment --comment fturn-raw-experiment -j ACCEPT
rule filter FORWARD -i ftraw0 -o ens3 -s 10.77.0.2/32 -m comment --comment fturn-raw-experiment -j ACCEPT
rule filter FORWARD -i ens3 -o ftraw0 -d 10.77.0.2/32 -m conntrack --ctstate ESTABLISHED,RELATED -m comment --comment fturn-raw-experiment -j ACCEPT
rule nat POSTROUTING -s 10.77.0.2/32 -o ens3 -m comment --comment fturn-raw-experiment -j MASQUERADE
