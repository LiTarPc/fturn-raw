#!/usr/bin/env bash
# FturnRaw 1.0.0 quick-start for Linux + systemd. Downloads verified release binaries.
set -Eeuo pipefail
VERSION=1.0.0
REPOSITORY=LiTarPc/fturn-raw
SERVICE=fturn-raw.service
INSTALL_DIR=/opt/fturn-raw
CONFIG_DIR=/etc/fturn-raw
UNIT_FILE=/etc/systemd/system/fturn-raw.service
SYSCTL_FILE=/etc/sysctl.d/90-fturn-raw-quick-start.conf
LOCK_FILE=/run/fturn-raw-quick-start.lock
PORT=56010
MTU=1420
DEVICES=3
INTERFACE=
PUBLIC_IP=
VK_LINK=
ACTION=install
CUSTOM_CONFIG=0
PORT_SET=0
MTU_SET=0
DEVICES_SET=0
INTERFACE_SET=0
TMP_DIR=
TRANSACTION=0
FORWARD_BEFORE=
ENABLED_BY_US=0

say() { printf '%s\n' "$*"; }
die() { printf 'Ошибка: %s\n' "$*" >&2; exit 1; }
usage() {
 cat <<'HELP'
FturnRaw 1.0.0: быстрая установка Linux-сервера (Debian/Ubuntu, systemd).

  sudo bash quick-start.sh
  sudo bash quick-start.sh --public-ip 203.0.113.10 --vk-link 'https://vk.ru/call/join/...'

  --port N          UDP-порт сервера (по умолчанию 56010)
  --devices N       Одновременных устройств, 1..64 (по умолчанию 3)
  --mtu N           MTU, 576..1500 (по умолчанию 1420; одинаковый на клиентах)
  --interface NAME  Внешний интерфейс (по умолчанию определяется по маршруту)
  --public-ip IPv4  Доступный клиентам IPv4, особенно если VPS находится за NAT
  --vk-link URL     Вывести готовую fturnraw:// ссылку; нужен --public-ip
  --check           Только проверить систему и показать план; без установки пакетов
  --uninstall       Удалить установку этого скрипта, сохранив raw.key
  --help            Справка

Настройки: UDP 56010, TUN ftraw0, 10.77.0.1/24, профиль auto, до 3 устройств.
Ключ хранится в /etc/fturn-raw/raw.key. Повторный запуск сохраняет ключ.
Чужая установка не перезаписывается. Для изменения параметров сначала --uninstall.
HELP
}
parse_args() {
 while (($#)); do
  case "$1" in
   --help|-h) usage; exit 0 ;;
   --check) ACTION=check; shift ;;
   --uninstall) ACTION=uninstall; shift ;;
   --port|--devices|--mtu|--interface|--public-ip|--vk-link)
    (($# >= 2)) || die "Не указано значение $1"
    [[ -n "$2" ]] || die "Пустое значение $1"
    case "$1" in
     --port) PORT=$2; PORT_SET=1; CUSTOM_CONFIG=1 ;;
     --devices) DEVICES=$2; DEVICES_SET=1; CUSTOM_CONFIG=1 ;;
     --mtu) MTU=$2; MTU_SET=1; CUSTOM_CONFIG=1 ;;
     --interface) INTERFACE=$2; INTERFACE_SET=1; CUSTOM_CONFIG=1 ;;
     --public-ip) PUBLIC_IP=$2 ;;
     --vk-link) VK_LINK=$2 ;;
    esac
    shift 2 ;;
   *) die "Неизвестный параметр: $1 (справка: --help)" ;;
  esac
 done
 local value
 for value in "$PORT" "$MTU" "$DEVICES"; do
  [[ "$value" =~ ^[0-9]{1,5}$ ]] || die 'Порт, MTU и число устройств должны быть целыми числами'
 done
 PORT=$((10#$PORT)); MTU=$((10#$MTU)); DEVICES=$((10#$DEVICES))
 ((PORT >= 1 && PORT <= 65535 && MTU >= 576 && MTU <= 1500 && DEVICES >= 1 && DEVICES <= 64)) || die 'Порт: 1..65535; MTU: 576..1500; устройств: 1..64'
 [[ -z "$INTERFACE" || "$INTERFACE" =~ ^[a-zA-Z0-9_.:-]{1,15}$ ]] || die 'Некорректное имя интерфейса'
 [[ -z "$VK_LINK" || -n "$PUBLIC_IP" ]] || die 'Для --vk-link укажите --public-ip'
}
require_root() { [[ $(id -u) == 0 ]] || die 'Запустите скрипт через sudo или от root'; }
require_systemd() { [[ -d /run/systemd/system ]] || die 'Нужен Linux с запущенным systemd'; }
require_tun() {
 if [[ "$ACTION" != check && ! -c /dev/net/tun ]] && command -v modprobe >/dev/null; then modprobe tun || true; fi
 [[ -c /dev/net/tun ]] || die 'Нет /dev/net/tun: включите TUN у хостера или в настройках контейнера'
}
ensure_dependencies() {
 local missing=0 cmd
 for cmd in curl unzip python3 ip iptables ss sysctl systemctl systemd-analyze sha256sum flock; do
  command -v "$cmd" >/dev/null || missing=1
 done
 if ((missing)); then
  [[ "$ACTION" != check ]] || die 'Не хватает curl/unzip/python3/iproute2/iptables/systemd/util-linux; --check не устанавливает пакеты'
  command -v apt-get >/dev/null || die 'Установите curl unzip python3 iproute2 iptables systemd util-linux вручную'
  say 'Устанавливаю необходимые пакеты...'
  DEBIAN_FRONTEND=noninteractive apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl unzip python3 iproute2 iptables systemd util-linux
 fi
}
check_paths() {
 local path mode
 for path in "$INSTALL_DIR" "$CONFIG_DIR"; do
  if [[ -e "$path" ]]; then
   [[ -d "$path" && ! -L "$path" && $(stat -c %u "$path") == 0 ]] || die "Каталог должен принадлежать root: $path"
   mode=$(stat -c %a "$path")
   (( (8#$mode & 0022) == 0 )) || die "Каталог доступен на запись другим пользователям: $path"
  fi
 done
 for path in "$INSTALL_DIR" "$CONFIG_DIR" "$UNIT_FILE" "$SYSCTL_FILE" "$CONFIG_DIR/raw.key" "$CONFIG_DIR/quick-start.json"; do
  [[ ! -L "$path" ]] || die "Символьная ссылка вместо служебного пути: $path"
 done
 if [[ ! -f "$CONFIG_DIR/quick-start.json" ]]; then
  for path in "$INSTALL_DIR/raw-server" "$INSTALL_DIR/raw-firewall" "$UNIT_FILE" "$SYSCTL_FILE"; do
   [[ ! -e "$path" ]] || die "Найдена существующая установка: $path. Quick-start её не перезаписывает; см. инструкцию обновления"
  done
 fi
}
validate_inputs() {
 python3 - "$PUBLIC_IP" "$VK_LINK" <<'PY'
import ipaddress,sys,urllib.parse
if sys.argv[1]:
    ip=ipaddress.IPv4Address(sys.argv[1])
    assert not (ip.is_unspecified or ip.is_multicast or ip.is_loopback or int(ip)==2**32-1), 'Нужен unicast IPv4'
if sys.argv[2]:
    u=urllib.parse.urlsplit(sys.argv[2])
    assert u.scheme=='https' and u.netloc in ('vk.ru','vk.com') and u.path.startswith('/call/join/') and len(u.path)>11, 'Нужна ссылка https://vk.ru/call/join/...'
    assert not any(ord(c)<32 or ord(c)==127 for c in sys.argv[2]), 'Недопустимые символы ссылки'
PY
}
verify_managed() {
 python3 - "$CONFIG_DIR/quick-start.json" "$INSTALL_DIR" "$UNIT_FILE" "$SYSCTL_FILE" <<'PY'
import hashlib,json,pathlib,sys
p=pathlib.Path(sys.argv[1]);assert p.is_file() and not p.is_symlink()
c=json.loads(p.read_text());assert c['managedBy']=='fturn-raw-quick-start' and c['version']=='1.0.0'
files={'binary':pathlib.Path(sys.argv[2])/'raw-server','firewall':pathlib.Path(sys.argv[2])/'raw-firewall','unit':pathlib.Path(sys.argv[3]),'sysctl':pathlib.Path(sys.argv[4])}
for name,path in files.items():
    assert path.is_file() and not path.is_symlink() and hashlib.sha256(path.read_bytes()).hexdigest()==c['hashes'][name], 'Файл изменён вручную: '+str(path)
PY
 [[ "$(systemctl show "$SERVICE" --property=FragmentPath --value)" == "$UNIT_FILE" ]] || die 'Служба загружена из другого файла; требуется ручная проверка'
 [[ -z "$(systemctl show "$SERVICE" --property=DropInPaths --value)" ]] || die 'Найдены дополнительные настройки службы; требуется ручная проверка'
}
show_connection() {
 python3 - "$CONFIG_DIR/quick-start.json" "$CONFIG_DIR/raw.key" "$PUBLIC_IP" "$VK_LINK" <<'PY'
import base64,ipaddress,json,pathlib,sys
c=json.loads(pathlib.Path(sys.argv[1]).read_text());key=pathlib.Path(sys.argv[2]).read_text().strip()
assert len(key)==64 and len(bytes.fromhex(key))==32, 'Некорректный ключ'
host=sys.argv[3] or c['publicIP'];endpoint=f"{host}:{c['port']}"
print('\nRaw server '+c['version']+' работает. Автозапуск включён.')
print('Сервер: '+endpoint+' (UDP)')
if not ipaddress.IPv4Address(host).is_global:
    print('Это адрес интерфейса. При NAT используйте доступный извне IPv4 (--public-ip).')
print('Устройств одновременно: '+str(c['devices']))
print('MTU: '+str(c['mtu'])+'; профиль сервера: auto; профиль клиента: rtpopus2')
print('Ключ: '+key)
print('Файл ключа: '+sys.argv[2])
if sys.argv[4]:
    wire={'v':1,'server':endpoint,'vk':sys.argv[4],'key':key,'mtu':c['mtu'],'streams':10,'mode':'full'}
    print('\nСсылка импорта (содержит ключ):')
    print('fturnraw://'+base64.urlsafe_b64encode(json.dumps(wire,separators=(',',':')).encode()).decode().rstrip('='))
else:
    print('Для готовой ссылки повторите запуск с --public-ip IPv4 --vk-link URL.')
PY
}
cleanup() {
 local rc=$1
 trap - EXIT INT TERM HUP
 if ((TRANSACTION)); then
  say 'Установка не завершена. Убираю созданную службу и её правила...' >&2
  set +e
  systemctl stop "$SERVICE" >/dev/null 2>&1
  if [[ -x "$INSTALL_DIR/raw-firewall" ]]; then "$INSTALL_DIR/raw-firewall" down; fi
  if ((ENABLED_BY_US)); then systemctl disable "$SERVICE" >/dev/null 2>&1; fi
  rm -f -- "$UNIT_FILE" "$SYSCTL_FILE" "$INSTALL_DIR/raw-server" "$INSTALL_DIR/raw-firewall" "$CONFIG_DIR/quick-start.json"
  systemctl daemon-reload
  if [[ "$FORWARD_BEFORE" == 0 ]]; then sysctl -w net.ipv4.ip_forward=0 >/dev/null; fi
  # The key is deliberately preserved, including after an interrupted install.
  rmdir -- "$INSTALL_DIR" 2>/dev/null || true
 fi
 if [[ -n "$TMP_DIR" && -d "$TMP_DIR" ]]; then rm -rf -- "$TMP_DIR"; fi
 exit "$rc"
}
write_firewall() {
 cat > "$TMP_DIR/raw-firewall" <<EOF
#!/bin/sh
set -eu
mode=\${1:-}
case "\$mode" in up|down) ;; *) exit 2;; esac
if [ "\$mode" = up ]; then
 [ "\$(sysctl -n net.ipv4.ip_forward)" = 1 ] || { echo 'IPv4 forwarding is disabled' >&2; exit 1; }
fi
rule() {
 table=\$1; chain=\$2; shift 2
 if [ "\$mode" = up ]; then
  iptables -w -t "\$table" -C "\$chain" "\$@" 2>/dev/null || iptables -w -t "\$table" -I "\$chain" 1 "\$@"
 elif iptables -w -t "\$table" -C "\$chain" "\$@" 2>/dev/null; then
  iptables -w -t "\$table" -D "\$chain" "\$@"
 fi
}
rule filter INPUT -p udp --dport $PORT -m comment --comment fturn-raw-quick-start -j ACCEPT
for last in \$(seq 2 $((DEVICES+1))); do
 client_ip=10.77.0.\$last
 rule filter INPUT -i ftraw0 -s "\$client_ip/32" -d 10.77.0.1/32 -m comment --comment fturn-raw-quick-start -j ACCEPT
 rule filter FORWARD -i ftraw0 -o '$INTERFACE' -s "\$client_ip/32" -m comment --comment fturn-raw-quick-start -j ACCEPT
 rule filter FORWARD -i '$INTERFACE' -o ftraw0 -d "\$client_ip/32" -m conntrack --ctstate ESTABLISHED,RELATED -m comment --comment fturn-raw-quick-start -j ACCEPT
 rule nat POSTROUTING -s "\$client_ip/32" -o '$INTERFACE' -m comment --comment fturn-raw-quick-start -j MASQUERADE
done
EOF
}
write_unit() {
 cat > "$TMP_DIR/fturn-raw.service" <<EOF
[Unit]
Description=FturnRaw server $VERSION (quick-start)
Wants=network-online.target
After=network-online.target ufw.service
ConditionPathExists=/dev/net/tun

[Service]
Type=simple
ExecStartPre=$INSTALL_DIR/raw-firewall up
ExecStart=$INSTALL_DIR/raw-server -listen 0.0.0.0:$PORT -tun ftraw0 -address 10.77.0.1/24 -client-ip 10.77.0.2 -max-devices $DEVICES -mtu $MTU -obf-profile auto -obf-key-file $CONFIG_DIR/raw.key
ExecStopPost=$INSTALL_DIR/raw-firewall down
Restart=on-failure
RestartSec=5
TimeoutStopSec=15
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
CapabilityBoundingSet=CAP_NET_ADMIN
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
}
ready() {
 systemctl is-active --quiet "$SERVICE" || return 1
 ip -j -4 address show dev ftraw0 | python3 -c 'import json,sys; a=json.load(sys.stdin); assert a[0]["mtu"]==int(sys.argv[1]) and any(x.get("local")=="10.77.0.1" and x.get("prefixlen")==24 for x in a[0]["addr_info"])' "$MTU" || return 1
 local pid
 pid=$(systemctl show "$SERVICE" --property=MainPID --value)
 [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
 ss -H -ulnp "sport = :$PORT" | grep -F "pid=$pid," >/dev/null
}
main() {
 parse_args "$@"
 require_root; require_systemd
 umask 077
 check_paths
 ensure_dependencies
 if [[ "$ACTION" != check ]]; then
  exec 9>"$LOCK_FILE"
  flock -n 9 || die 'Уже запущен другой quick-start'
 fi
 check_paths
 validate_inputs
 if [[ -f "$CONFIG_DIR/quick-start.json" ]]; then
  verify_managed
  if [[ "$ACTION" == uninstall ]]; then
   systemctl stop "$SERVICE"
   "$INSTALL_DIR/raw-firewall" down
   systemctl disable "$SERVICE"
   rm -f -- "$UNIT_FILE" "$SYSCTL_FILE" "$INSTALL_DIR/raw-server" "$INSTALL_DIR/raw-firewall" "$CONFIG_DIR/quick-start.json"
   systemctl daemon-reload
   rmdir -- "$INSTALL_DIR" 2>/dev/null || true
   say "Служба и её правила удалены. Ключ сохранён: $CONFIG_DIR/raw.key"
   say 'IPv4 forwarding автоматически не выключается: он может использоваться другими службами.'
   return
  fi
  if ((CUSTOM_CONFIG)); then
   python3 - "$CONFIG_DIR/quick-start.json" "$PORT" "$PORT_SET" "$MTU" "$MTU_SET" "$DEVICES" "$DEVICES_SET" "$INTERFACE" "$INTERFACE_SET" <<'PY'
import json,sys
c=json.load(open(sys.argv[1]))
for name,value,explicit in [('port',int(sys.argv[2]),sys.argv[3]),('mtu',int(sys.argv[4]),sys.argv[5]),('devices',int(sys.argv[6]),sys.argv[7]),('interface',sys.argv[8],sys.argv[9])]:
    assert explicit!='1' or c[name]==value, 'Параметры отличаются. Сначала --uninstall (ключ сохранится), затем установите заново.'
PY
  fi
  systemctl is-active --quiet "$SERVICE" || die "Служба установлена, но не работает. Проверьте journalctl -u $SERVICE"
  if [[ "$ACTION" == check ]]; then say 'Управляемая установка найдена; файлы и служба проверены.'; else show_connection; fi
  return
 fi
 [[ "$ACTION" != uninstall ]] || die 'Установка этого скрипта не найдена'
 [[ -z "$(systemctl show "$SERVICE" --property=FragmentPath --value)" ]] || die 'Уже существует другая служба fturn-raw; требуется ручное обновление'
 [[ -z "$(systemctl show "$SERVICE" --property=DropInPaths --value)" ]] || die 'У службы уже есть дополнительные настройки'
 require_tun
 local arch route
 case $(uname -m) in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; *) die 'Поддерживаются только Linux amd64 и arm64';; esac
 route=$(ip -j -4 route get 1.1.1.1)
 if [[ -z "$INTERFACE" ]]; then INTERFACE=$(python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["dev"])' <<<"$route"); fi
 [[ "$INTERFACE" =~ ^[a-zA-Z0-9_.:-]{1,15}$ && "$INTERFACE" != ftraw0 ]] || die 'Не удалось определить внешний интерфейс'
 ip link show dev "$INTERFACE" >/dev/null
 if [[ -z "$PUBLIC_IP" ]]; then PUBLIC_IP=$(python3 -c 'import json,sys; r=json.load(sys.stdin)[0]; print(r.get("prefsrc") or r.get("src") or "")' <<<"$route"); fi
 [[ -n "$PUBLIC_IP" ]] || die 'Не удалось определить IPv4; укажите --public-ip'
 validate_inputs
 if ip link show dev ftraw0 >/dev/null 2>&1; then die 'Интерфейс ftraw0 уже используется'; fi
 [[ -z "$(ss -H -ulnp "sport = :$PORT")" ]] || die "UDP-порт $PORT занят"
 ip -j -4 route show | python3 -c 'import ipaddress,json,sys; pool=ipaddress.ip_network("10.77.0.0/24"); assert not any(r.get("dst", "default")!="default" and ipaddress.ip_network(r["dst"],strict=False).overlaps(pool) for r in json.load(sys.stdin)), "Сеть 10.77.0.0/24 уже используется; требуется ручная настройка адресов"'
 FORWARD_BEFORE=$(sysctl -n net.ipv4.ip_forward)
 [[ "$FORWARD_BEFORE" == 0 || "$FORWARD_BEFORE" == 1 ]] || die 'Не удалось прочитать IPv4 forwarding'
 say "План: Raw $VERSION, linux-$arch, UDP $PORT, MTU $MTU, устройств $DEVICES, выход $INTERFACE."
 if [[ "$ACTION" == check ]]; then say 'Проверка завершена. Изменений нет.'; return; fi
 TMP_DIR=$(mktemp -d -t fturn-raw-quick-start.XXXXXXXX)
 trap 'cleanup "$?"' EXIT
 trap 'exit 130' INT
 trap 'exit 143' TERM
 trap 'exit 129' HUP
 local archive base expected
 archive="fturn-raw-server-$VERSION-linux-$arch.zip"
 base="https://github.com/$REPOSITORY/releases/download/v$VERSION"
 say 'Скачиваю сервер и проверяю SHA-256...'
 curl -fL --retry 3 --connect-timeout 15 --max-time 180 --proto '=https' --tlsv1.2 -o "$TMP_DIR/$archive" "$base/$archive"
 curl -fL --retry 3 --connect-timeout 15 --max-time 60 --proto '=https' --tlsv1.2 -o "$TMP_DIR/SERVER-SHA256SUMS" "$base/SERVER-SHA256SUMS"
 expected=$(python3 - "$TMP_DIR/SERVER-SHA256SUMS" "$archive" <<'PY'
import pathlib,re,sys
rows=[line.split() for line in pathlib.Path(sys.argv[1]).read_text().splitlines()]
found=[r[0] for r in rows if len(r)==2 and r[1]==sys.argv[2] and re.fullmatch('[0-9a-fA-F]{64}',r[0])]
assert len(found)==1, 'Нет однозначной SHA-256 суммы архива'
print(found[0].lower())
PY
 )
 [[ "$(sha256sum "$TMP_DIR/$archive" | cut -d ' ' -f1)" == "$expected" ]] || die 'Контрольная сумма архива не совпадает; установка отменена'
 unzip -p "$TMP_DIR/$archive" raw-server > "$TMP_DIR/raw-server"
 python3 - "$TMP_DIR/raw-server" "$arch" <<'PY'
import pathlib,struct,sys
h=pathlib.Path(sys.argv[1]).read_bytes()[:20]
assert h[:6]==b'\x7fELF\x02\x01' and struct.unpack_from('<H',h,18)[0]=={'amd64':62,'arm64':183}[sys.argv[2]], 'Неверная архитектура бинарника'
PY
 chmod 0755 "$TMP_DIR/raw-server"
 [[ "$("$TMP_DIR/raw-server" --version)" == "FturnRaw server $VERSION" ]] || die 'Версия скачанного сервера не совпадает'
 write_firewall; write_unit
 sh -n "$TMP_DIR/raw-firewall"
 # Verify syntax before installing. ExecStart files are staged only after this step;
 # systemd-analyze also checks executables, so validate the installed unit below.
 if [[ -e "$CONFIG_DIR/raw.key" ]]; then
  [[ -f "$CONFIG_DIR/raw.key" && $(stat -c %u "$CONFIG_DIR/raw.key") == 0 ]] || die 'Ключ должен быть обычным файлом root'
  python3 - "$CONFIG_DIR/raw.key" <<'PY'
import pathlib,sys
k=pathlib.Path(sys.argv[1]).read_text().strip();assert len(k)==64 and len(bytes.fromhex(k))==32, 'Некорректный существующий ключ'
PY
 fi
 TRANSACTION=1
 install -d -m 0755 "$INSTALL_DIR"
 install -d -m 0700 "$CONFIG_DIR"
 install -m 0755 "$TMP_DIR/raw-server" "$INSTALL_DIR/raw-server"
 install -m 0755 "$TMP_DIR/raw-firewall" "$INSTALL_DIR/raw-firewall"
 if [[ ! -e "$CONFIG_DIR/raw.key" ]]; then "$INSTALL_DIR/raw-server" -gen-obf-key > "$CONFIG_DIR/raw.key"; fi
 chmod 0600 "$CONFIG_DIR/raw.key"
 install -m 0644 "$TMP_DIR/fturn-raw.service" "$UNIT_FILE"
 printf '%s\n' '# Managed by fturn-raw quick-start' 'net.ipv4.ip_forward=1' > "$SYSCTL_FILE"
 systemd-analyze verify "$UNIT_FILE"
 sysctl -w net.ipv4.ip_forward=1 >/dev/null
 systemctl daemon-reload
 ENABLED_BY_US=1
 systemctl enable --now "$SERVICE"
 local attempt
 for ((attempt=1; attempt<=15; attempt+=1)); do if ready 2>/dev/null; then break; fi; sleep 1; done
 ready || die "Сервер не запустился; проверьте journalctl -u $SERVICE"
 sleep 1
 ready || die 'Сервер остановился после запуска'
 python3 - "$CONFIG_DIR/quick-start.json" "$INSTALL_DIR" "$UNIT_FILE" "$SYSCTL_FILE" "$PORT" "$MTU" "$DEVICES" "$INTERFACE" "$PUBLIC_IP" <<'PY'
import hashlib,json,pathlib,sys
files={'binary':pathlib.Path(sys.argv[2])/'raw-server','firewall':pathlib.Path(sys.argv[2])/'raw-firewall','unit':pathlib.Path(sys.argv[3]),'sysctl':pathlib.Path(sys.argv[4])}
c={'managedBy':'fturn-raw-quick-start','version':'1.0.0','port':int(sys.argv[5]),'mtu':int(sys.argv[6]),'devices':int(sys.argv[7]),'interface':sys.argv[8],'publicIP':sys.argv[9],'hashes':{k:hashlib.sha256(p.read_bytes()).hexdigest() for k,p in files.items()}}
pathlib.Path(sys.argv[1]).write_text(json.dumps(c,indent=2)+'\n')
PY
 TRANSACTION=0
 show_connection
}
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
