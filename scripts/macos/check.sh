#!/bin/sh
# Checks for scripts/macos that run as any user. They install nothing, start
# nothing, and never call launchctl, route, ifconfig, wg, wireguard-go, or docker.
# Functions are cut out of the real scripts and exercised against fixed input.
set -u

PATH=/usr/bin:/bin:/usr/sbin:/sbin
export PATH

here=$(CDPATH= cd -- "$(/usr/bin/dirname "$0")" && pwd)
HELPER="$here/helper.sh"
SETUP="$here/setup.sh"
UNINSTALL="$here/uninstall.sh"
PLIST="$here/dev.whalias.tunnel.plist"
tmp=$(/usr/bin/mktemp -d "${TMPDIR:-/tmp}/whalias-check.XXXXXX")
: >"$tmp/fails"

pass() { printf 'ok   %s\n' "$*"; }
fail() {
	printf 'FAIL %s\n' "$*"
	printf '%s\n' "$*" >>"$tmp/fails"
}
check() {
	desc=$1
	shift
	if "$@"; then pass "$desc"; else fail "$desc"; fi
}
eq() { [ "$1" = "$2" ]; }

# Print the named top-level functions from a script.
extract() {
	file=$1
	shift
	for fn in "$@"; do
		/usr/bin/awk -v fn="$fn" '
			$0 == fn "() {" { p = 1 }
			p { print }
			p && $0 == "}" { exit }
		' "$file"
	done
}

code() { /usr/bin/grep -hv '^[[:space:]]*#' "$HELPER" "$SETUP" "$UNINSTALL"; }
absent() {
	if code | /usr/bin/grep -Eq -- "$1"; then return 1; fi
	return 0
}
present() { /usr/bin/grep -Fqx -- "$2" "$1"; }

printf '%s\n' '--- syntax'
for f in "$here"/*.sh; do
	check "sh -n $(/usr/bin/basename "$f")" /bin/sh -n "$f"
done
check "plutil -lint $(/usr/bin/basename "$PLIST")" /usr/bin/plutil -lint -s "$PLIST"

printf '%s\n' '--- plist'
pv() { /usr/libexec/PlistBuddy -c "Print :$1" "$PLIST" 2>/dev/null; }
no_key() { ! pv "$1" >/dev/null; }
check "Label dev.whalias.tunnel" eq "$(pv Label)" dev.whalias.tunnel
check "KeepAlive false" eq "$(pv KeepAlive)" false
check "RunAtLoad true" eq "$(pv RunAtLoad)" true
check "AbandonProcessGroup false" eq "$(pv AbandonProcessGroup)" false
check "no UserName" no_key UserName
check "no GroupName" no_key GroupName
check "no LaunchOnlyOnce, StartInterval, WatchPaths" eval 'no_key LaunchOnlyOnce && no_key StartInterval && no_key WatchPaths'
check "runs /bin/sh helper.sh" eq "$(pv ProgramArguments:0) $(pv ProgramArguments:1)" "/bin/sh /usr/local/libexec/whalias/helper.sh"

printf '%s\n' '--- source'
check "no kill, killall, or pkill" absent '(^|[^[:alnum:]_-])(kill|killall|pkill)([^[:alnum:]_-]|$)'
check "no PATH docker lookup" absent 'command -v docker|which docker|type docker'
check "no /usr/local/bin/docker" absent '/usr/local/bin/docker'
check "no bare docker call" absent '^[[:space:]]*docker[[:space:]]'
check "no GNU stat" absent '(^|[^/[:alnum:]_])stat[[:space:]]|stat -c'
check "no PATH date" absent '(^|[^/[:alnum:]_])date[[:space:]]'
check "no System keychain change" absent 'add-trusted-cert|add-certificates|System\.keychain'
check "no Docker extension" absent 'docker extension|extension install'
check "no lo0 alias" absent 'lo0'
check "no IP forwarding on the Mac" absent 'ip\.forwarding'
check "no Docker context change" absent 'context use|context create|context rm'
untyped=$(code | /usr/bin/grep -E 'run_docker|docker_cmd|DOCKER_BIN"' | /usr/bin/grep -E '(^|[^[:alnum:]_-])inspect([^[:alnum:]_-]|$)' | /usr/bin/grep -Ev '(container|image|network|volume|context) inspect')
check "every docker inspect names its object type" eq "$untyped" ""
check "helper looks the controller up by name with container inspect" eval "/usr/bin/grep -Fq -- \"run_docker container inspect -f '{{.Id}} {{.State.Running}} {{.Name}}' \\\"\\\$CONTAINER\\\"\" '$HELPER'"
check "helper checks mounts and port by container id and verifies .Id in output" eq "$(/usr/bin/grep -Ec 'run_docker container inspect -f '\''\{\{\.Id\}\} \{\{\.State\.Running\}\} \{\{\.Name\}\}.*"\$cid"' "$HELPER")" 2
check "helper does not call unverified docker port" eval "! /usr/bin/grep -Eq 'run_docker port ' '$HELPER'"
check "setup checks existing controller shape and publishes by container id" eq "$(/usr/bin/grep -Ec 'docker_cmd container inspect -f '\''\{\{\.Id\}\} .*"\$cid"' "$SETUP")" 4
check "setup does not call unverified docker port" eval "! /usr/bin/grep -Eq 'docker_cmd port ' '$SETUP'"
for f in "$HELPER" "$SETUP" "$UNINSTALL"; do
	check "$(/usr/bin/basename "$f") pins PATH" present "$f" 'PATH=/usr/bin:/bin:/usr/sbin:/sbin'
done
for f in "$HELPER" "$UNINSTALL"; do
	check "$(/usr/bin/basename "$f") STABLE_NET is the /24" present "$f" 'STABLE_NET="10.254.253.0/24"'
done
for f in "$HELPER" "$SETUP"; do
	check "$(/usr/bin/basename "$f") Docker Desktop CLI" present "$f" 'DOCKER_BIN="/Applications/Docker.app/Contents/Resources/bin/docker"'
	check "$(/usr/bin/basename "$f") mac.key outside /keys" eval "present '$f' 'PRIVKEY=\"/var/lib/whalias/mac.key\"' && present '$f' 'KEYDIR=\"/var/lib/whalias/keys\"'"
done
check "AllowedIPs" present "$HELPER" 'ALLOWED="10.254.254.2/32,10.254.253.0/24"'
check "wireguard-go path" present "$HELPER" 'WGGO_BIN="/opt/homebrew/bin/wireguard-go"'
check "wg path" present "$HELPER" 'WG_BIN="/opt/homebrew/bin/wg"'
check "wireguard-go -f utun" eval "/usr/bin/grep -Fq '\"\$WGGO_BIN\" -f utun >>' '$HELPER'"
check "WG_TUN_NAME_FILE exported" eval "/usr/bin/grep -Eq '^[[:space:]]*export WG_TUN_NAME_FILE\$' '$HELPER'"
routes=$(code | /usr/bin/grep -E 'route[^|]* add' | /usr/bin/sed 's/^[[:space:]]*//')
check "only route add is the stable net on the named utun" eq "$routes" '/sbin/route -n add -net "$STABLE_NET" -interface "$iface" || down'
check "setup mounts the VM docker socket" eval "/usr/bin/grep -Fq -- '-v \"/var/run/docker.sock:/var/run/docker.sock\"' '$SETUP'"
check "setup does not mount the Mac proxy socket" absent '\$\{?sock\}?:/var/run/docker\.sock'
pubs=$(/usr/bin/grep -E '^[[:space:]]*-p ' "$SETUP")
check "setup publishes three ports" eq "$(printf '%s\n' "$pubs" | /usr/bin/wc -l | /usr/bin/tr -d ' ')" 3
check "setup publishes on 127.0.0.1 only" eval "! printf '%s\n' \"\$pubs\" | /usr/bin/grep -vq -- '-p \"127\\.0\\.0\\.1:'"
check "setup does not set a restart policy" absent '--restart'
check "resolver body" eval "/usr/bin/grep -Fq \"printf '%s\\\\n' 'nameserver 127.0.0.1' 'port 5387'\" '$SETUP'"
check "helper unsets DOCKER_HOST" eval "/usr/bin/grep -Eq '^unset .*DOCKER_HOST' '$HELPER'"
check "setup unsets DOCKER_HOST" eval "/usr/bin/grep -Eq '^unset .*DOCKER_HOST' '$SETUP'"
check "user_context_host clears DOCKER_CONFIG under sudo" eval "/usr/bin/grep -Fq -- 'sudo -n -u \"\$user\" -- /usr/bin/env -u DOCKER_CONFIG' '$HELPER'"
check "mac.pub move uses -h against symlink-to-dir" eval "/usr/bin/grep -Fq -- '/bin/mv -h -f \"\$tmp\" \"\$KEYDIR/mac.pub\"' '$HELPER' && /usr/bin/grep -Fq -- '/bin/mv -h -f \"\$tmp\" \"\$KEYDIR/mac.pub\"' '$SETUP'"
check "uninstall does not execute on-disk helper" eval "! /usr/bin/grep -Eq '\"\\\$HELPER\"[[:space:]]+down' '$UNINSTALL'"

printf '%s\n' '--- helpers stay in step'
for fn in valid_utun valid_wg_key parse_route_iface has_tunnel_addr; do
	check "$fn same in uninstall.sh" eq "$(extract "$HELPER" "$fn")" "$(extract "$UNINSTALL" "$fn")"
done
for fn in valid_cid mode_is_gw_writable socket_url_ok user_context_host strip_cid_header parse_cid_mounts key_mount_ok mount_exposes_privkey resolve_host_mount_sources private_key_in_shared_dir; do
	check "$fn same in setup.sh" eq "$(extract "$HELPER" "$fn")" "$(extract "$SETUP" "$fn")"
done

printf '%s\n' '--- helper functions'
(
	TUN_MAC="10.254.254.1"
	eval "$(extract "$HELPER" log valid_utun valid_wg_key valid_cid parse_route_iface has_tunnel_addr strip_cid_header parse_cid_mounts parse_udp_port)"

	good_cid="612fa0d10259a7c83317241d6eca2e0f33bad01a6fdeac1ea232d2181280e1bd"
	other_cid="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	check "64-hex container id accepted" valid_cid "$good_cid"
	for bad_id in '' 'a' '612fa0d10259' "${good_cid%d}" "${good_cid}0" "612FA0D10259A7C83317241D6ECA2E0F33BAD01A6FDEAC1EA232D2181280E1BD" "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"; do
		check "invalid container id '$bad_id' rejected" eval "! valid_cid '$bad_id'"
	done
	check "container id with newline rejected" eval '! valid_cid "$(printf "%s\n%s" "$good_cid" "$other_cid")"'

	port_ok=$(printf '%s true /whalias\n127.0.0.1:55001\n' "$good_cid")
	port_decoy=$(printf '%s true /%s\n127.0.0.1:44444\n' "$other_cid" "$good_cid")
	port_stopped=$(printf '%s false /whalias\n127.0.0.1:55001\n' "$good_cid")
	port_renamed=$(printf '%s true /evil\n127.0.0.1:55001\n' "$good_cid")
	check "strip_cid_header accepts matching id/running/name" eq "$(printf '%s\n' "$port_ok" | strip_cid_header "$good_cid true /whalias" | parse_udp_port)" 55001
	check "strip_cid_header rejects decoy container named with cid" eval '! printf "%s\n" "$port_decoy" | strip_cid_header "$good_cid true /whalias" >/dev/null'
	check "strip_cid_header rejects stopped container" eval '! printf "%s\n" "$port_stopped" | strip_cid_header "$good_cid true /whalias" >/dev/null'
	check "strip_cid_header rejects renamed container" eval '! printf "%s\n" "$port_renamed" | strip_cid_header "$good_cid true /whalias" >/dev/null'
	check "strip_cid_header rejects empty output" eval '! printf "" | strip_cid_header "$good_cid true /whalias" >/dev/null'

	mounts_ok=$(printf '%s true /whalias\n/keys\tbind\t/host_mnt/private/var/lib/whalias/keys\n/data\tvolume\t/var/lib/docker/volumes/whalias-data/_data\n' "$good_cid")
	mounts_decoy=$(printf '%s true /%s\n/keys\tbind\t/host_mnt/private/var/lib/whalias/keys\n' "$other_cid" "$good_cid")
	mounts_badline=$(printf '%s true /whalias\n/keys\tbind\t/tmp/a\textra\n' "$good_cid")
	check "parse_cid_mounts extracts /keys mount for matching cid" eq "$(printf '%s\n' "$mounts_ok" | parse_cid_mounts "$good_cid true /whalias" keys)" "bind:/host_mnt/private/var/lib/whalias/keys"
	check "parse_cid_mounts rejects decoy container named with cid" eval '! printf "%s\n" "$mounts_decoy" | parse_cid_mounts "$good_cid true /whalias" keys >/dev/null'
	check "parse_cid_mounts rejects malformed mount line" eval '! printf "%s\n" "$mounts_badline" | parse_cid_mounts "$good_cid true /whalias" sources >/dev/null'
	check "parse_cid_mounts rejects empty output" eval '! printf "" | parse_cid_mounts "$good_cid true /whalias" keys >/dev/null'

	check "utun8 is a utun" valid_utun utun8
	for bad in utun en0 'utun1 x' 'utun1;id' '../utun1' 'utun1/' 'xutun1'; do
		check "'$bad' is not a utun" eval "! valid_utun '$bad'"
	done
	check "utun name with a newline rejected" eval '! valid_utun "$(printf "utun1\nutun2")"'

	key=$(/bin/dd if=/dev/urandom bs=32 count=1 2>/dev/null | /usr/bin/base64)
	check "random 32-byte key is valid" valid_wg_key "$key"
	check "42 A + A= is valid" valid_wg_key "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	check "42 A + B= is not a 32-byte key" eval '! valid_wg_key "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB="'
	check "short key rejected" eval '! valid_wg_key "AAAA="'
	check "key with newline payload rejected" eval '! valid_wg_key "$(printf "%s\nx" "$key")"'

	stable='   route to: 10.254.253.1
destination: 10.254.253.0
       mask: 255.255.255.0
  interface: utun8
      flags: <UP,DONE,STATIC>'
	vpn_default='   route to: 10.254.253.1
destination: default
       mask: default
    gateway: 100.64.0.1
  interface: utun3'
	wide='   route to: 10.254.253.1
destination: 10.254.0.0
       mask: 255.255.0.0
  interface: utun4'
	lan='   route to: 10.254.253.1
destination: 10.254.253.0
       mask: 255.255.255.0
  interface: en0'
	check "stable route names utun8" eq "$(printf '%s\n' "$stable" | parse_route_iface)" utun8
	check "VPN default route names nothing" eq "$(printf '%s\n' "$vpn_default" | parse_route_iface)" ""
	check "wider 10.254/16 route names nothing" eq "$(printf '%s\n' "$wide" | parse_route_iface)" ""
	check "stable net on en0 names nothing" eq "$(printf '%s\n' "$lan" | parse_route_iface)" ""

	ours='utun8: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1280
	inet 10.254.254.1 --> 10.254.254.2 netmask 0xff000000'
	other='utun3: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1280
	inet 100.64.0.5 --> 100.64.0.5 netmask 0xffffffff'
	near='utun5: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1280
	inet 10.254.254.10 --> 10.254.254.2 netmask 0xff000000'
	check "utun with 10.254.254.1 is ours" eval 'printf "%s\n" "$ours" | has_tunnel_addr'
	check "another VPN utun is not ours" eval '! printf "%s\n" "$other" | has_tunnel_addr'
	check "10.254.254.10 is not ours" eval '! printf "%s\n" "$near" | has_tunnel_addr'

	check "loopback publish read" eq "$(printf '127.0.0.1:55001\n' | parse_udp_port)" 55001
	check "0.0.0.0 publish ignored" eq "$(printf '0.0.0.0:55001\n' | parse_udp_port)" ""
	check "IPv6 line skipped" eq "$(printf '[::1]:5\n127.0.0.1:61000\n' | parse_udp_port)" 61000
	check "port out of range ignored" eq "$(printf '127.0.0.1:99999\n' | parse_udp_port)" ""
	check "trailing colon payload ignored" eq "$(printf '127.0.0.1:55001:extra\n' | parse_udp_port)" ""
)

printf '%s\n' '--- teardown ownership & route checks'
(
	eval "$(extract "$UNINSTALL" has_stable_route)"
	stable_utun='destination: 10.254.253.0
mask: 255.255.255.0
interface: utun8'
	stable_en0='destination: 10.254.253.0
mask: 255.255.255.0
interface: en0'
	vpn_def='destination: default
mask: default
interface: utun3'
	wide_net='destination: 10.254.0.0
mask: 255.255.0.0
interface: utun4'
	check "has_stable_route detects utun /24" eval 'printf "%s\n" "$stable_utun" | has_stable_route'
	check "has_stable_route detects en0 /24" eval 'printf "%s\n" "$stable_en0" | has_stable_route'
	check "has_stable_route ignores default" eval '! printf "%s\n" "$vpn_def" | has_stable_route'
	check "has_stable_route ignores /16" eval '! printf "%s\n" "$wide_net" | has_stable_route'

	iface=""
	IFACE_FILE="$tmp/stale.iface"
	TUN_NAME_FILE="$tmp/real.tun"
	printf 'utun3\n' >"$IFACE_FILE"
	printf 'utun8\n' >"$TUN_NAME_FILE"
	eval "$(extract "$HELPER" log valid_utun pick_owned_utun)"
	owned_utun() { [ "$1" = "utun8" ]; }
	check "helper pick_owned_utun skips stale non-owned utun" eq "$(pick_owned_utun utun9 2>/dev/null)" utun8
	/bin/rm -f "$TUN_NAME_FILE"
	check "helper pick_owned_utun falls through to route utun" eq "$(pick_owned_utun utun8 2>/dev/null)" utun8
	check "helper pick_owned_utun returns nothing when none owned" eq "$(pick_owned_utun utun9 2>/dev/null)" ""

	printf 'utun8\n' >"$TUN_NAME_FILE"
	eval "$(extract "$UNINSTALL" pick_owned_utun)"
	check "uninstall pick_owned_utun skips stale non-owned utun" eq "$(pick_owned_utun utun9 2>/dev/null)" utun8
)

printf '%s\n' '--- user_context_host validation'
(
	DOCKER_BIN="/bin/echo"
	eval "$(extract "$HELPER" user_context_host)"
	for bad in '' root -u '../etc' 'bad/user' 'bad user' 'whalias_nonexistent_user_xyz'; do
		check "user_context_host rejects '$bad'" eval "! user_context_host '$bad' >/dev/null"
	done
)

printf '%s\n' '--- shared key directory'
(
	KEYDIR="$tmp/keys"
	PRIVKEY="$tmp/mac.key"
	/bin/mkdir -m 755 "$KEYDIR"
	/bin/dd if=/dev/urandom bs=32 count=1 2>/dev/null | /usr/bin/base64 >"$PRIVKEY"
	eval "$(extract "$HELPER" valid_wg_key keydir_ok private_key_in_shared_dir read_controller_key)"

	check "empty key directory is clean" eval '! private_key_in_shared_dir'
	/bin/dd if=/dev/urandom bs=32 count=1 2>/dev/null | /usr/bin/base64 >"$KEYDIR/mac.pub"
	check "public keys only is clean" eval '! private_key_in_shared_dir'
	/bin/cp "$PRIVKEY" "$KEYDIR/copy.pub"
	check "private key under a .pub name is caught" private_key_in_shared_dir
	/bin/rm -f "$KEYDIR/copy.pub"
	: >"$KEYDIR/wg0.key"
	check ".key file is caught" private_key_in_shared_dir
	/bin/rm -f "$KEYDIR/wg0.key"
	: >"$KEYDIR/.mac.key"
	check "dotfile .mac.key is caught" private_key_in_shared_dir
	/bin/rm -f "$KEYDIR/.mac.key"
	printf 'PrivateKey = x\n' >"$KEYDIR/peer.txt"
	check "wg config text is caught" private_key_in_shared_dir
	/bin/rm -f "$KEYDIR/peer.txt"
	printf 'privatekey = x\n' >"$KEYDIR/peer.txt"
	check "lowercase privatekey text is caught" private_key_in_shared_dir
	/bin/rm -f "$KEYDIR/peer.txt"
	printf -- '-----BEGIN PRIVATE KEY-----\n' >"$KEYDIR/peer.txt"
	check "PEM private key text is caught" private_key_in_shared_dir
	/bin/rm -f "$KEYDIR/peer.txt"
	/bin/ln -s "$tmp/nonexistent" "$KEYDIR/dangling"
	check "dangling symlink in key dir is caught" private_key_in_shared_dir
	/bin/rm -f "$KEYDIR/dangling"
	/bin/mkdir "$KEYDIR/sub"
	check "subdirectory in key dir is caught" private_key_in_shared_dir
	/bin/rmdir "$KEYDIR/sub"
	/usr/bin/mkfifo "$KEYDIR/pipe"
	check "FIFO in key dir is caught without hanging" private_key_in_shared_dir
	/bin/rm -f "$KEYDIR/pipe"

	check "user-owned 755 key dir accepted" keydir_ok
	/bin/chmod 750 "$KEYDIR"
	check "user-owned 750 key dir accepted" keydir_ok
	/bin/chmod 775 "$KEYDIR"
	check "group-writable 775 key dir rejected" eval '! keydir_ok'
	/bin/chmod 777 "$KEYDIR"
	check "world-writable 777 key dir rejected" eval '! keydir_ok'
	/bin/chmod 2755 "$KEYDIR"
	check "setgid 2755 key dir rejected" eval '! keydir_ok'
	/bin/chmod 755 "$KEYDIR"
	/bin/ln -s "$KEYDIR" "$tmp/keys-link"
	KEYDIR="$tmp/keys-link"
	check "symlink key dir rejected" eval '! keydir_ok'
	KEYDIR="$tmp/keys-missing"
	check "missing key dir rejected" eval '! keydir_ok'
	KEYDIR="/usr/bin"
	check "root-owned key dir rejected" eval '! keydir_ok'
	KEYDIR="$tmp/keys"

	check "missing controller.pub rejected" eval '! read_controller_key >/dev/null'
	/bin/ln -s "$PRIVKEY" "$KEYDIR/controller.pub"
	check "symlinked controller.pub rejected" eval '! read_controller_key >/dev/null'
	/bin/rm -f "$KEYDIR/controller.pub"
	printf 'not a key\n' >"$KEYDIR/controller.pub"
	check "garbage controller.pub rejected" eval '! read_controller_key >/dev/null'
	good=$(/bin/dd if=/dev/urandom bs=32 count=1 2>/dev/null | /usr/bin/base64)
	printf '%s\n' "$good" >"$KEYDIR/controller.pub"
	check "valid controller.pub read" eq "$(read_controller_key)" "$good"
)

printf '%s\n' '--- container mount checks'
(
	eval "$(extract "$HELPER" key_mount_ok mount_exposes_privkey resolve_host_mount_sources)"
	for ok_m in 'bind:/var/lib/whalias/keys' 'bind:/private/var/lib/whalias/keys' 'bind:/private/var/lib/whalias/keys/' \
		'bind:/host_mnt/var/lib/whalias/keys' 'bind:/host_mnt/private/var/lib/whalias/keys' 'bind:/host_mnt/private/var/lib/whalias/keys/'; do
		check "key_mount_ok accepts $ok_m" eval "printf '%s\n' '$ok_m' | key_mount_ok"
	done
	for bad_m in '' 'volume:/var/lib/whalias/keys' 'bind:/tmp/evil/var/lib/whalias/keys' 'bind:/var/lib/whalias' \
		'bind:/host_mnt/private/var/lib/whalias' 'bind:/host_mnt/tmp/evil/var/lib/whalias/keys' \
		'bind:/host_mnt/var/lib/whalias' 'bind:/host_mnt' 'bind:/host_mnt/private/var/lib/whalias/keys/mac.pub' \
		'bind:/host_mnt/private/var/lib/whalias/keys2' 'bind:/host_mnt/private/var/lib/whalias/keys/..' \
		'bind:/host_mnt/private/var/lib/whalias/keys/.' 'bind:/host_mnt/host_mnt/private/var/lib/whalias/keys' \
		'bind:/host_mnt//private/var/lib/whalias/keys' 'bind:/HOST_MNT/private/var/lib/whalias/keys' \
		'bind: /host_mnt/private/var/lib/whalias/keys' 'volume:/host_mnt/private/var/lib/whalias/keys'; do
		check "key_mount_ok rejects '$bad_m'" eval "! printf '%s\n' '$bad_m' | key_mount_ok"
	done
	check "key_mount_ok rejects duplicate /keys mounts" eval "! printf 'bind:/var/lib/whalias/keys\nbind:/var/lib/whalias/keys\n' | key_mount_ok"

	for bad_s in '/' '/.' '/private' '/var' '/private/var' '/var/lib' '/private/var/lib' \
		'/var/lib/whalias' '/private/var/lib/whalias' '/var/lib/whalias/.' '/var/lib/./whalias' \
		'/var/lib/whalias/keys/..' '/var/lib/whalias/mac.key' '/var/lib/whalias/docker.host' '/var/lib/whalias/docker-config' \
		'/host_mnt/private/var/lib/whalias' '/host_mnt/private/var/lib/whalias/mac.key' '/host_mnt' \
		'/host_mnt/var/lib/whalias' '/host_mnt/private' '/host_mnt/private/var/lib/whalias/keys/../mac.key' \
		'/host_mnt/host_mnt/private/var/lib/whalias' 'var/lib/whalias' \
		'/VAR/lib/whalias' '/private/var/lib/WHALIAS' '/var/lib/whalias/MAC.KEY' '/Users/x/Mac.Key' \
		'/host_mnt/Private/Var/Lib/Whalias' '/host_mnt/private/var/lib/whalias/Docker-Config' \
		'/System' '/System/Volumes' '/System/Volumes/Data' '/System/Volumes/Data/private' \
		'/System/Volumes/Data/private/var/lib/whalias' '/System/Volumes/Data/private/var/lib/whalias/docker-config' \
		'/host_mnt/System/Volumes/Data/private/var/lib/whalias' '/Volumes'; do
		check "mount_exposes_privkey catches $bad_s" eval "printf '%s\n' '$bad_s' | mount_exposes_privkey"
	done
	# APFS Unicode case-folding bypasses (U+017F long s -> s, U+212A Kelvin -> k) and control bytes.
	check "mount_exposes_privkey catches APFS U+017F /var/lib/whalias fold" eval "printf '/var/lib/whalia\305\277\n' | mount_exposes_privkey"
	check "mount_exposes_privkey catches APFS U+212A mac.key fold" eval "printf '/Users/x/mac.\342\204\252ey\n' | mount_exposes_privkey"
	check "mount_exposes_privkey catches control byte in path" eval "printf '/tmp/a\tb\n' | mount_exposes_privkey"
	for ok_s in '/var/run/docker.sock' '/run/host-services/docker.proxy.sock' '/var/lib/whalias/keys' '/private/var/lib/whalias/keys' '/var/lib/docker/volumes/whalias-data/_data' \
		'/host_mnt/private/var/lib/whalias/keys' '/host_mnt/var/lib/whalias/keys'; do
		check "mount_exposes_privkey allows $ok_s" eval "! printf '%s\n' '$ok_s' | mount_exposes_privkey"
	done

	/bin/mkdir -m 755 "$tmp/safe-dir"
	/bin/ln -s "$tmp/safe-dir" "$tmp/safe-link"
	/bin/ln -s /var/lib/whalias "$tmp/evil-link"
	/bin/ln -s /var/lib/whalias/future-mac-key "$tmp/dangling-evil"
	check "host symlink to /var/lib/whalias caught" eval "printf '%s\n' '$tmp/evil-link' | resolve_host_mount_sources | mount_exposes_privkey"
	check "host_mnt symlink to /var/lib/whalias caught" eval "printf '/host_mnt%s\n' '$tmp/evil-link' | resolve_host_mount_sources | mount_exposes_privkey"
	check "nonexistent child under host symlink to /var/lib/whalias caught" eval "printf '%s/sub\n' '$tmp/evil-link' | resolve_host_mount_sources | mount_exposes_privkey"
	check "dangling symlink into /var/lib/whalias caught" eval "printf '%s\n' '$tmp/dangling-evil' | resolve_host_mount_sources | mount_exposes_privkey"
	if [ -L '/Volumes/Macintosh HD' ]; then
		check "/Volumes/Macintosh HD symlink to / caught" eval "printf '/Volumes/Macintosh HD\n' | resolve_host_mount_sources | mount_exposes_privkey"
	fi
	check "safe host symlink allowed" eval "! printf '%s\n' '$tmp/safe-link' | resolve_host_mount_sources | mount_exposes_privkey"
	check "VM-only docker proxy socket allowed after host resolution" eval "! printf '/run/host-services/docker.proxy.sock\n' | resolve_host_mount_sources | mount_exposes_privkey"
	check "VM-only volume path allowed after host resolution" eval "! printf '/var/lib/docker/volumes/whalias-data/_data\n' | resolve_host_mount_sources | mount_exposes_privkey"
)

printf '%s\n' '--- docker deadline (fake docker, about 10 s)'
(
	set -eu
	DOCKER_HOST="unix:///nonexistent.sock"
	RUNDIR="$tmp"
	eval "$(extract "$HELPER" log run_docker)"
	printf '#!/bin/sh\nprintf "abc true\\n"\n' >"$tmp/docker-ok"
	printf '#!/bin/sh\nexit 1\n' >"$tmp/docker-fail"
	printf '#!/bin/sh\n/bin/sleep 14\n' >"$tmp/docker-hang"
	/bin/chmod 755 "$tmp/docker-ok" "$tmp/docker-fail" "$tmp/docker-hang"

	DOCKER_BIN="$tmp/docker-ok"
	check "answering docker output returned" eq "$(run_docker inspect x)" "abc true"
	DOCKER_BIN="$tmp/docker-fail"
	t0=$(/bin/date +%s)
	set +e
	run_docker inspect x >/dev/null 2>&1
	rc=$?
	set -e
	t1=$(/bin/date +%s)
	check "failing docker is not inspectable" eq "$rc" 1
	check "failing docker returns promptly under set -eu" eval "[ $((t1 - t0)) -le 2 ]"
	DOCKER_BIN="$tmp/docker-hang"
	t0=$(/bin/date +%s)
	set +e
	out=$(run_docker inspect x 2>/dev/null)
	rc=$?
	set -e
	t1=$(/bin/date +%s)
	check "hung docker is not inspectable" eq "$rc:$out" "1:"
	check "hung docker gives up within 12 s, not when it exits" eval "[ $((t1 - t0)) -le 12 ]"
	DOCKER_BIN="$tmp/docker-ok"
	check "call after timed-out docker is not clobbered" eq "$(run_docker inspect x)" "abc true"
)

printf '%s\n' '--- root-owned install chain'
(
	eval "$(extract "$SETUP" die mode_is_gw_writable require_root_chain)"
	for m in 775 777 1777 77 22 2 '' abc; do
		check "mode '$m' flagged writable/invalid" mode_is_gw_writable "$m"
	done
	for m in 755 750 700 644 600 0 1755; do
		check "mode '$m' not group/world writable" eval "! mode_is_gw_writable '$m'"
	done
	for d in /Library/LaunchDaemons /private/etc/resolver /private/var/lib /usr/local; do
		if [ -d "$d" ]; then
			check "$d chain is root-only" eval "(require_root_chain '$d') 2>/dev/null"
		fi
	done
	check "a user-owned directory is refused" eval "! (require_root_chain '$tmp') 2>/dev/null"
	check "setup refuses the group-writable /private/var/run" eval "! (require_root_chain /private/var/run) 2>/dev/null"
)

printf '%s\n' '--- helper run-directory chain'
(
	eval "$(extract "$HELPER" mode_is_gw_writable daemon_group_has_extra etc_group_daemon_has_users daemon_has_users chain_entry_ok require_root_chain)"

	clean_grp='PrimaryGroupID: 1
GroupMembers: FFFFEEEE-DDDD-CCCC-BBBB-AAAA00000000
GroupMembership: root'
	extra_mem='PrimaryGroupID: 1
GroupMembers: FFFFEEEE-DDDD-CCCC-BBBB-AAAA00000000
GroupMembership: root alice'
	extra_ml='PrimaryGroupID: 1
GroupMembership:
 root
 alice'
	extra_uuid='PrimaryGroupID: 1
GroupMembers: FFFFEEEE-DDDD-CCCC-BBBB-AAAA00000000 11111111-2222-3333-4444-555555555555
GroupMembership: root'
	nested_grp='PrimaryGroupID: 1
GroupMembership: root
NestedGroups:
 ABCDEFAB-CDEF-ABCD-EFAB-CDEF00000014'
	check "clean daemon group record accepted" eval '! printf "%s\n" "$clean_grp" | daemon_group_has_extra'
	check "empty daemon group record fails closed" eval 'printf "" | daemon_group_has_extra'
	check "extra GroupMembership rejected" eval 'printf "%s\n" "$extra_mem" | daemon_group_has_extra'
	check "multiline extra GroupMembership rejected" eval 'printf "%s\n" "$extra_ml" | daemon_group_has_extra'
	check "extra GroupMembers UUID rejected" eval 'printf "%s\n" "$extra_uuid" | daemon_group_has_extra'
	check "NestedGroups on daemon rejected" eval 'printf "%s\n" "$nested_grp" | daemon_group_has_extra'

	check "/etc/group daemon:*:1:root accepted" eval '! printf "daemon:*:1:root\n" | etc_group_daemon_has_users'
	check "/etc/group daemon:*:1: accepted" eval '! printf "daemon:*:1:\n" | etc_group_daemon_has_users'
	check "/etc/group daemon:*:1:root,alice rejected" eval 'printf "daemon:*:1:root,alice\n" | etc_group_daemon_has_users'
	check "/etc/group missing gid 1 fails closed" eval 'printf "wheel:*:0:root\n" | etc_group_daemon_has_users'

	daemon_has_users() { return 1; }
	run=/private/var/run/whalias
	check "root:wheel 0755 parent accepted" chain_entry_ok /private/var "$run" 0 0 0755
	check "/private/var/run root:daemon 0775 accepted as parent of $run" chain_entry_ok /private/var/run "$run" 0 1 0775
	check "/private/var/run refused as the directory passed in" eval '! chain_entry_ok /private/var/run /private/var/run 0 1 0775'
	check "/private/var/run refused as parent of non-whalias dir" eval '! chain_entry_ok /private/var/run /private/var/run/other 0 1 0775'
	for e in '0 0 0775' '0 20 0775' '0 1 0777' '0 1 0757' '0 1 2775' '0 1 4775' '0 1 1775' '502 1 0775' '502 1 0755'; do
		check "/private/var/run uid gid mode '$e' refused" eval "! chain_entry_ok /private/var/run '$run' $e"
	done
	for e in '0 1 0775' '0 0 0775' '0 0 0777' '0 0 0757' '502 20 0755' '502 20 0700'; do
		check "whalias run dir uid gid mode '$e' refused" eval "! chain_entry_ok '$run' '$run' $e"
	done
	check "another root:daemon 0775 parent refused" eval "! chain_entry_ok /private/var '$run' 0 1 0775"
	check "/private/var/run/x root:daemon 0775 parent refused" eval "! chain_entry_ok /private/var/run/x '$run/y' 0 1 0775"
	daemon_has_users() { return 0; }
	check "/private/var/run refused when an ordinary login is in daemon" eval "! chain_entry_ok /private/var/run '$run' 0 1 0775"
	eval "$(extract "$HELPER" daemon_has_users)"
	check "no ordinary login is in daemon on this Mac" eval '! daemon_has_users'
	check "helper refuses /private/var/run itself" eval '! require_root_chain /private/var/run'
	check "helper refuses a user-owned directory" eval "! require_root_chain '$tmp'"
	if [ -d "$run" ]; then
		check "helper accepts the $run chain" require_root_chain "$run"
	fi
)

printf '%s\n' '--- setup subnet overlap'
(
	eval "$(extract "$SETUP" subnets_overlap_tunnel)"
	for s in 10.254.253.0/24 10.254.0.0/16 10.0.0.0/8 10.254.254.0/30 10.254.254.2/32 0.0.0.0/0 '172.18.0.0/16 10.254.253.128/25'; do
		check "$s overlaps" eval "printf '%s\n' '$s' | subnets_overlap_tunnel"
	done
	for s in 172.18.0.0/16 192.168.65.0/24 10.254.252.0/24 10.254.254.4/30 fd00::/64 '' 'a.b.c.d/0' '999.0.0.1/24'; do
		check "'$s' does not overlap" eval "! printf '%s\n' '$s' | subnets_overlap_tunnel"
	done
)

printf '%s\n' '--- install state (information only)'
for p in /Library/LaunchDaemons/dev.whalias.tunnel.plist /usr/local/libexec/whalias/helper.sh /etc/resolver/wha.internal /var/lib/whalias; do
	if [ -e "$p" ]; then
		printf 'info %s is installed\n' "$p"
	else
		printf 'info %s is not installed\n' "$p"
	fi
done

fails=$(/usr/bin/wc -l <"$tmp/fails" | /usr/bin/tr -d ' ')
/bin/rm -rf "$tmp"
if [ "$fails" -ne 0 ]; then
	printf '%s check(s) failed\n' "$fails"
	exit 1
fi
printf '%s\n' 'all checks passed'
