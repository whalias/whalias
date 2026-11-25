#!/bin/sh
# Root helper for the macOS Whalias tunnel. launchd label dev.whalias.tunnel runs this.
# The kernel names the utun. WG_TUN_NAME_FILE receives that name. The only installed
# route is 10.254.253.0/24. When the controller is not running, the controller was
# replaced, Docker cannot be inspected, or wireguard-go or the utun disappears, this
# process removes that route, the WireGuard socket, and the utun, then exits.
# KeepAlive is false, so launchd leaves it down.
set -eu

PATH=/usr/bin:/bin:/usr/sbin:/sbin
LC_ALL=C
export PATH LC_ALL
unset DOCKER_HOST DOCKER_CONTEXT DOCKER_CERT_PATH DOCKER_TLS_VERIFY

LABEL="dev.whalias.tunnel"
DOCKER_BIN="/Applications/Docker.app/Contents/Resources/bin/docker"
WG_BIN="/opt/homebrew/bin/wg"
WGGO_BIN="/opt/homebrew/bin/wireguard-go"
CONTAINER="whalias"
STABLE_NET="10.254.253.0/24"
TUN_MAC="10.254.254.1"
TUN_CTR="10.254.254.2"
ALLOWED="10.254.254.2/32,10.254.253.0/24"
WG_PORT="51820"

STATE="/var/lib/whalias"
KEYDIR="/var/lib/whalias/keys"
PRIVKEY="/var/lib/whalias/mac.key"
HOSTFILE="/var/lib/whalias/docker.host"
DOCKER_CONFIG="/var/lib/whalias/docker-config"
export DOCKER_CONFIG
RUNDIR="/var/run/whalias"
TUN_NAME_FILE="/var/run/whalias/tun.name"
IFACE_FILE="/var/run/whalias/iface"
LOG_DIR="/var/log/whalias"
WG_LOG="/var/log/whalias/wireguard-go.log"

iface=""
pid=""
cid=""
stopping=0
DOCKER_HOST=""

log() {
	printf '%s %s\n' "$(/bin/date '+%Y-%m-%dT%H:%M:%S')" "$*" >&2
}

valid_utun() {
	case "$1" in
	utun*) unit=${1#utun} ;;
	*) return 1 ;;
	esac
	case "$unit" in
	'' | *[!0-9]*) return 1 ;;
	esac
	return 0
}

valid_wg_key() {
	case "$1" in
	*[!A-Za-z0-9+/=]*) return 1 ;;
	esac
	printf '%s\n' "$1" | /usr/bin/grep -Eq '^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw048]=$'
}

valid_cid() {
	case "$1" in
	*[!0-9a-f]*) return 1 ;;
	esac
	[ "${#1}" -eq 64 ]
}

# Reads `route -n get` output. Prints the interface only when the matched route is
# the stable subnet itself on a utun, so a default route's interface is never named.
parse_route_iface() {
	/usr/bin/awk '
		$1 == "destination:" { dest = $2 }
		$1 == "mask:" { mask = $2 }
		$1 == "interface:" { nic = $2 }
		END {
			if (dest == "10.254.253.0" && mask == "255.255.255.0" && nic ~ /^utun[0-9]+$/) print nic
		}
	'
}

iface_from_route() {
	/sbin/route -n get 10.254.253.1 2>/dev/null | parse_route_iface || true
}

# Reads `ifconfig <utun>` output. Succeeds when the Mac tunnel address is on it.
has_tunnel_addr() {
	/usr/bin/awk -v want="$TUN_MAC" '
		$1 == "inet" && $2 == want { found = 1 }
		END { exit found ? 0 : 1 }
	'
}

# A utun named by a leftover file may now belong to another VPN or to a wg-quick
# tunnel. Only one this helper started, one carrying 10.254.254.1, or one keyed with
# this Mac's private key is torn down.
owned_utun() {
	valid_utun "$1" || return 1
	if [ -n "$iface" ] && [ "$1" = "$iface" ]; then
		return 0
	fi
	if /sbin/ifconfig "$1" 2>/dev/null | has_tunnel_addr; then
		return 0
	fi
	if [ -f "$PRIVKEY" ] && [ ! -L "$PRIVKEY" ] && [ -s "$PRIVKEY" ] && [ -x "$WG_BIN" ]; then
		want=$("$WG_BIN" pubkey <"$PRIVKEY" 2>/dev/null || true)
		have=$("$WG_BIN" show "$1" public-key 2>/dev/null || true)
		if valid_wg_key "$want" && [ "$want" = "$have" ]; then
			return 0
		fi
	fi
	return 1
}

# Return the first owned Whalias utun among the active interface, state files, and
# the stable route. A stale state file naming another VPN's utun is logged and
# skipped so it neither destroys that VPN nor hides a real Whalias utun.
pick_owned_utun() {
	route_nic=$1
	if valid_utun "$iface" && owned_utun "$iface"; then
		printf '%s\n' "$iface"
		return 0
	fi
	for f in "$IFACE_FILE" "$TUN_NAME_FILE"; do
		if [ -f "$f" ] && [ ! -L "$f" ]; then
			cand=$(/usr/bin/head -n 1 "$f" | /usr/bin/tr -d '[:space:]')
			if valid_utun "$cand"; then
				if owned_utun "$cand"; then
					printf '%s\n' "$cand"
					return 0
				fi
				log "$cand is not a Whalias utun. It is left alone."
			fi
		fi
	done
	if valid_utun "$route_nic"; then
		if owned_utun "$route_nic"; then
			printf '%s\n' "$route_nic"
			return 0
		fi
		log "$route_nic is not a Whalias utun. It is left alone."
	fi
	return 0
}

# Delete the stable route, remove the userspace socket, and destroy the utun.
# This does not signal wireguard-go. Removing its socket is what stops it.
down() {
	if [ "$stopping" = 1 ]; then
		return 0
	fi
	stopping=1
	trap '' EXIT TERM INT HUP
	set +e
	route_nic=$(iface_from_route || true)
	name=$(pick_owned_utun "$route_nic")
	log "teardown ${name:-no utun}"
	/sbin/route -n delete -net "$STABLE_NET" >/dev/null 2>&1
	if [ -d "$RUNDIR" ] && [ ! -L "$RUNDIR" ]; then
		/bin/rm -f "$RUNDIR"/docker.* "$RUNDIR"/ctx.*
	fi
	if valid_utun "$name"; then
		/bin/rm -f "/var/run/wireguard/${name}.sock"
		n=0
		while [ "$n" -lt 10 ]; do
			if [ -z "$pid" ] || ! /bin/ps -p "$pid" >/dev/null 2>&1; then
				break
			fi
			/bin/sleep 0.2
			n=$((n + 1))
		done
		n=0
		while [ "$n" -lt 10 ]; do
			/sbin/ifconfig "$name" >/dev/null 2>&1 || break
			/sbin/ifconfig "$name" destroy >/dev/null 2>&1
			/bin/sleep 0.2
			n=$((n + 1))
		done
		if /sbin/ifconfig "$name" >/dev/null 2>&1; then
			log "$name is still present"
		else
			if [ -d "$RUNDIR" ] && [ ! -L "$RUNDIR" ]; then
				/bin/rm -f "$IFACE_FILE" "$TUN_NAME_FILE"
			fi
			log "$name is gone"
		fi
	else
		if [ -d "$RUNDIR" ] && [ ! -L "$RUNDIR" ]; then
			/bin/rm -f "$IFACE_FILE" "$TUN_NAME_FILE"
		fi
	fi
	exit 0
}

# Docker CLI call with a 10 second deadline. Per-call temp files keep a timed-out
# background call from clobbering a later one. The helper never signals the stuck
# client; launchd reaps the job's process group after the helper exits.
run_docker() {
	out=$(/usr/bin/mktemp "${RUNDIR}/docker.XXXXXX") || return 1
	rc="${out}.rc"
	(
		trap - EXIT TERM INT HUP
		set +e
		"$DOCKER_BIN" --host "$DOCKER_HOST" "$@" >"$out" 2>/dev/null
		printf '%s\n' "$?" >"$rc"
	) </dev/null >/dev/null 2>&1 &
	n=0
	while [ ! -s "$rc" ]; do
		if [ "$n" -ge 50 ]; then
			log "docker $1 did not answer within 10 seconds"
			return 1
		fi
		/bin/sleep 0.2
		n=$((n + 1))
	done
	status=$(/usr/bin/head -n 1 "$rc")
	if [ "$status" != 0 ]; then
		/bin/rm -f "$out" "$rc"
		return 1
	fi
	/bin/cat "$out"
	/bin/rm -f "$out" "$rc"
}

mode_is_gw_writable() {
	case "$1" in
	'' | *[!0-7]*) return 0 ;;
	esac
	p="000$1"
	perm=${p#"${p%???}"}
	case "$perm" in
	?[2367]? | ??[2367]) return 0 ;;
	esac
	return 1
}

# Reads `dscl . -read /Groups/daemon` output. Succeeds when the daemon group record
# is missing PrimaryGroupID 1, has any NestedGroups, or names any member other than root.
daemon_group_has_extra() {
	LC_ALL=C /usr/bin/awk '
		BEGIN { seen_gid = 0; bad = 0; key = "" }
		/^[A-Za-z0-9_]+:/ {
			key = $1
			sub(/:$/, "", key)
			val = $0
			sub(/^[A-Za-z0-9_]+:[[:space:]]*/, "", val)
		}
		/^[[:space:]]/ {
			val = $0
			sub(/^[[:space:]]+/, "", val)
		}
		!/^[A-Za-z0-9_]+:/ && !/^[[:space:]]/ {
			key = ""
			val = ""
			next
		}
		{
			n = split(val, tok, /[[:space:]]+/)
			if (key == "PrimaryGroupID") {
				if (n == 1 && tok[1] == "1") seen_gid = 1
				else if (n > 0) bad = 1
			} else if (key == "GroupMembership") {
				for (i = 1; i <= n; i++) {
					if (tok[i] != "" && tok[i] != "root") bad = 1
				}
			} else if (key == "GroupMembers") {
				for (i = 1; i <= n; i++) {
					if (tok[i] != "" && toupper(tok[i]) != "FFFFEEEE-DDDD-CCCC-BBBB-AAAA00000000") bad = 1
				}
			} else if (key == "NestedGroups") {
				for (i = 1; i <= n; i++) {
					if (tok[i] != "") bad = 1
				}
			}
		}
		END { exit (!seen_gid || bad) ? 0 : 1 }
	'
}

# Reads /etc/group. Succeeds when gid 1 is missing or lists any member other than root.
etc_group_daemon_has_users() {
	LC_ALL=C /usr/bin/awk -F: '
		/^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
		$3 == "1" {
			seen = 1
			n = split($4, m, ",")
			for (i = 1; i <= n; i++) {
				if (m[i] != "" && m[i] != "root") bad = 1
			}
		}
		END { exit (!seen || bad) ? 0 : 1 }
	'
}

# Succeeds when any non-root user (ordinary login, hidden account, nested group,
# or console user) belongs to group daemon (gid 1), or when membership cannot be
# proven empty. Fails closed on empty/invalid directory output or tool errors.
daemon_has_users() {
	grp=$(/usr/bin/dscl . -read /Groups/daemon 2>/dev/null) || return 0
	if printf '%s\n' "$grp" | daemon_group_has_extra; then
		return 0
	fi
	glist=$(/usr/bin/dscl . -list /Groups PrimaryGroupID 2>/dev/null) || return 0
	if ! printf '%s\n' "$glist" | LC_ALL=C /usr/bin/awk '
		NF >= 2 {
			gid = $NF
			if (gid !~ /^-?[0-9]+$/) { bad = 1; next }
			if (gid == 1) {
				if ($0 ~ /^daemon[[:space:]]+1[[:space:]]*$/) seen = 1
				else bad = 1
			}
		}
		END { exit (seen && !bad) ? 0 : 1 }
	'; then
		return 0
	fi
	pglist=$(/usr/bin/dscl . -list /Users PrimaryGroupID 2>/dev/null) || return 0
	if ! printf '%s\n' "$pglist" | LC_ALL=C /usr/bin/awk '
		NF >= 2 {
			gid = $NF
			if (gid !~ /^-?[0-9]+$/) { bad = 1; next }
			if ($0 ~ /^root[[:space:]]+0[[:space:]]*$/) seen_root = 1
			if (gid == 1 && $0 !~ /^daemon[[:space:]]+1[[:space:]]*$/) bad = 1
		}
		END { exit (seen_root && !bad) ? 0 : 1 }
	'; then
		return 0
	fi
	if [ ! -r /etc/group ] || etc_group_daemon_has_users </etc/group; then
		return 0
	fi
	list=$(/usr/bin/dscl . -list /Users UniqueID 2>/dev/null) || return 0
	if ! printf '%s\n' "$list" | LC_ALL=C /usr/bin/awk '
		NF >= 2 {
			if ($NF !~ /^-?[0-9]+$/) bad = 1
			if ($0 ~ /^root[[:space:]]+0[[:space:]]*$/) seen_root = 1
		}
		END { exit (seen_root && !bad) ? 0 : 1 }
	'; then
		return 0
	fi
	uids=$(printf '%s\n' "$list" | LC_ALL=C /usr/bin/awk '$NF ~ /^[0-9]+$/ && $NF >= 500 { print $NF }')
	console=$(/usr/bin/stat -f '%u' /dev/console 2>/dev/null) || return 0
	case "$console" in
	'' | *[!0-9]*) return 0 ;;
	esac
	checked=0
	for u in $uids $console; do
		case "$u" in
		0) continue ;;
		'' | *[!0-9]*) return 0 ;;
		esac
		r=$(/usr/bin/dsmemberutil checkmembership -u "$u" -g 1 2>/dev/null) || return 0
		if [ "$r" != "user is not a member of the group" ]; then
			return 0
		fi
		checked=1
	done
	[ "$checked" -eq 1 ] || return 0
	return 1
}

# Arguments: directory, target directory, uid, gid, 4-digit octal mode (%Mp%Lp).
# Root-owned and not group- or world-writable. macOS ships /private/var/run as
# root:daemon 0775; that is accepted only as a parent of /private/var/run/whalias,
# never as the directory passed in, never with setuid/setgid/sticky bits, and only
# while no non-root user is in daemon.
chain_entry_ok() {
	if [ "$3" != 0 ]; then
		return 1
	fi
	if ! mode_is_gw_writable "$5"; then
		return 0
	fi
	[ "$1" = /private/var/run ] && [ "$2" = /private/var/run/whalias ] && [ "$4" = 1 ] && [ "$5" = 0775 ] && ! daemon_has_users
}

require_root_chain() {
	start=$(CDPATH= cd -- "$1" && pwd -P) || return 1
	target=${2:-$start}
	dir=$start
	while :; do
		info=$(/usr/bin/stat -f '%u %g %Mp%Lp' "$dir" 2>/dev/null) || return 1
		set -- $info
		chain_entry_ok "$dir" "$target" "${1:-}" "${2:-}" "${3:-}" || return 1
		if [ "$dir" = / ]; then
			break
		fi
		dir=$(/usr/bin/dirname "$dir")
	done
}

socket_url_ok() {
	case "$1" in
	unix:///*) [ -S "${1#unix://}" ] ;;
	*) return 1 ;;
	esac
}

# The desktop-linux endpoint from the Docker Desktop CLI, run as that user so a
# user-writable ~/.docker (plugins, hooks, credential helpers) never runs as root.
user_context_host() {
	user=$1
	case "$user" in
	'' | root | -* | *[!A-Za-z0-9._-]*) return 1 ;;
	esac
	uid=$(/usr/bin/id -u "$user" 2>/dev/null || true)
	case "$uid" in
	'' | 0 | *[!0-9]*) return 1 ;;
	esac
	home=$(/usr/bin/dscl . -read "/Users/$user" NFSHomeDirectory 2>/dev/null | /usr/bin/sed -n 's/^NFSHomeDirectory: //p' | /usr/bin/head -n 1 || true)
	if [ -z "$home" ] || [ ! -d "$home" ]; then
		return 1
	fi
	out=$(/usr/bin/sudo -n -u "$user" -- /usr/bin/env -u DOCKER_CONFIG -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_CERT_PATH -u DOCKER_TLS_VERIFY HOME="$home" "$DOCKER_BIN" context inspect desktop-linux --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)
	printf '%s\n' "$out" | /usr/bin/head -n 1 | /usr/bin/tr -d '[:space:]'
}

user_context_host_timed() {
	out=$(/usr/bin/mktemp "${RUNDIR}/ctx.XXXXXX") || return 1
	rc="${out}.rc"
	(
		trap - EXIT TERM INT HUP
		set +e
		user_context_host "$1" >"$out" 2>/dev/null
		printf '%s\n' "$?" >"$rc"
	) </dev/null >/dev/null 2>&1 &
	n=0
	while [ ! -s "$rc" ]; do
		if [ "$n" -ge 50 ]; then
			log "docker context inspect did not answer within 10 seconds"
			return 1
		fi
		/bin/sleep 0.2
		n=$((n + 1))
	done
	status=$(/usr/bin/head -n 1 "$rc")
	if [ "$status" != 0 ]; then
		/bin/rm -f "$out" "$rc"
		return 1
	fi
	/bin/cat "$out"
	/bin/rm -f "$out" "$rc"
}

# setup.sh records the endpoint it read from desktop-linux. That record comes first.
discover_docker_host() {
	if [ -f "$HOSTFILE" ] && [ ! -L "$HOSTFILE" ]; then
		info=$(/usr/bin/stat -f '%u %Lp' "$HOSTFILE" 2>/dev/null || true)
		set -- $info
		if [ "${1:-}" = 0 ] && ! mode_is_gw_writable "${2:-}"; then
			found=$(/usr/bin/head -n 1 "$HOSTFILE" | /usr/bin/tr -d '[:space:]')
			if socket_url_ok "$found"; then
				printf '%s\n' "$found"
				return 0
			fi
		fi
	fi
	found=$(user_context_host_timed "$(/usr/bin/stat -f '%Su' /dev/console 2>/dev/null || true)" || true)
	if socket_url_ok "$found"; then
		printf '%s\n' "$found"
		return 0
	fi
	return 1
}

# Prints "<container id> <running> <name>".
container_state() {
	run_docker container inspect -f '{{.Id}} {{.State.Running}} {{.Name}}' "$CONTAINER"
}

# The same container must still be running. A replaced container is a gone one.
check_container() {
	st=$(container_state || true)
	if ! valid_cid "$cid" || [ "$st" != "$cid true /${CONTAINER}" ]; then
		log "controller $CONTAINER is not running, was replaced, or Docker could not be inspected"
		down
	fi
}

check_tunnel() {
	if ! /bin/ps -p "$pid" >/dev/null 2>&1 || ! /sbin/ifconfig "$iface" >/dev/null 2>&1; then
		log "wireguard-go or $iface is gone"
		down
	fi
}

# Verify header line matches $1 and print remaining lines.
strip_cid_header() {
	LC_ALL=C /usr/bin/awk -v want="$1" '
		NR == 1 {
			if ($0 != want) { bad = 1; exit 1 }
			seen = 1
			next
		}
		{ print }
		END { exit (seen && !bad) ? 0 : 1 }
	'
}

# Verify header line matches $1 and extract mount fields from "<dest>\t<type>\t<source>" lines.
parse_cid_mounts() {
	LC_ALL=C /usr/bin/awk -v want="$1" -v mode="$2" '
		NR == 1 {
			if ($0 != want) { bad = 1; exit 1 }
			seen = 1
			next
		}
		$0 == "" { next }
		{
			if (split($0, f, "\t") != 3) { bad = 1; exit 1 }
			if (mode == "keys") {
				if (f[1] == "/keys") print f[2] ":" f[3]
			} else if (mode == "sources") {
				print f[3]
			} else if (mode == "dests") {
				print f[1]
			}
		}
		END { exit (seen && !bad) ? 0 : 1 }
	'
}

# Reads "<HostIp>:<HostPort>" lines. Prints the 127.0.0.1 host port only.
parse_udp_port() {
	LC_ALL=C /usr/bin/awk -F: 'NF == 2 && $1 == "127.0.0.1" && $2 ~ /^[0-9]+$/ && $2 > 0 && $2 < 65536 { print $2; exit }'
}

published_udp_port() {
	raw=$(run_docker container inspect -f '{{.Id}} {{.State.Running}} {{.Name}}{{printf "\n"}}{{range index .NetworkSettings.Ports "'"${WG_PORT}"'/udp"}}{{.HostIp}}:{{.HostPort}}{{printf "\n"}}{{end}}' "$cid") || return 1
	lines=$(printf '%s\n' "$raw" | strip_cid_header "$cid true /${CONTAINER}") || return 1
	printf '%s\n' "$lines" | parse_udp_port
}

# The container writes this file into a directory the desktop user owns, so it is
# read only as a plain file holding one WireGuard public key.
read_controller_key() {
	f="$KEYDIR/controller.pub"
	if [ ! -f "$f" ] || [ -L "$f" ]; then
		return 1
	fi
	key=$(/usr/bin/head -c 128 "$f" | /usr/bin/tr -d '[:space:]')
	valid_wg_key "$key" || return 1
	printf '%s\n' "$key"
}

key_mount_ok() {
	/usr/bin/awk '
		BEGIN { count = 0; ok = 1 }
		NF == 0 { next }
		{
			count++
			s = $0
			gsub(/\/+$/, "", s)
			if (s != "bind:/var/lib/whalias/keys" && s != "bind:/private/var/lib/whalias/keys" && \
			    s != "bind:/host_mnt/var/lib/whalias/keys" && s != "bind:/host_mnt/private/var/lib/whalias/keys") {
				ok = 0
			}
		}
		END { exit (count == 1 && ok) ? 0 : 1 }
	'
}

mount_exposes_privkey() {
	LC_ALL=C /usr/bin/awk '
		function norm(p) {
			gsub(/\/+/, "/", p)
			while (gsub(/\/\.\//, "/", p) > 0) {}
			while (p ~ /\/\.$/) {
				sub(/\/\.$/, "", p)
				if (p == "") p = "/"
			}
			while (length(p) > 1 && p ~ /\/$/) {
				sub(/\/$/, "", p)
			}
			while (p ~ /^\/host_mnt(\/|$)/ || p ~ /^\/system\/volumes\/data(\/|$)/) {
				if (p ~ /^\/host_mnt(\/|$)/) {
					sub(/^\/host_mnt/, "", p)
				} else {
					sub(/^\/system\/volumes\/data/, "", p)
				}
				if (p == "") p = "/"
			}
			return p
		}
		BEGIN { bad = 0 }
		{
			if ($0 == "") next
			# Reject non-ASCII and control bytes so APFS Unicode case-folding (e.g. U+017F, U+212A)
			# cannot slip past ASCII tolower().
			if ($0 ~ /[^ -~]/) { bad = 1; next }
			# APFS is case-insensitive by default, so /VAR/LIB/WHALIAS is the same directory.
			s = tolower($0)
			if (s ~ /(^|\/)\.\.(\/|$)/) { bad = 1; next }
			if (s ~ /(^|\/)mac\.key(\/|$)/) { bad = 1; next }
			p = norm(s)
			if (p !~ /^\//) { bad = 1; next }
			if (p == "/" || p == "/private" || p == "/var" || p == "/private/var" || \
			    p == "/var/lib" || p == "/private/var/lib" || \
			    p == "/var/lib/whalias" || p == "/private/var/lib/whalias" || \
			    p == "/system" || p == "/system/volumes" || p == "/volumes") {
				bad = 1; next
			}
			if ((p ~ /^\/var\/lib\/whalias\// || p ~ /^\/private\/var\/lib\/whalias\//) && \
			    p != "/var/lib/whalias/keys" && p != "/private/var/lib/whalias/keys") {
				bad = 1; next
			}
		}
		END { exit bad ? 0 : 1 }
	'
}

# Emit each mount source plus its resolved Mac host path when the path (or an
# existing ancestor) resolves through a symlink on the host.
resolve_host_mount_sources() {
	while IFS= read -r src || [ -n "$src" ]; do
		if [ -z "$src" ]; then
			continue
		fi
		printf '%s\n' "$src"
		hp=$src
		while :; do
			case "$hp" in
			/[Hh][Oo][Ss][Tt]_[Mm][Nn][Tt]) hp="/" ;;
			/[Hh][Oo][Ss][Tt]_[Mm][Nn][Tt]/*) hp="/${hp#/[Hh][Oo][Ss][Tt]_[Mm][Nn][Tt]/}" ;;
			*) break ;;
			esac
		done
		while :; do
			case "$hp" in
			/) break ;;
			*/) hp=${hp%/} ;;
			*) break ;;
			esac
		done
		case "$hp" in
		/*) ;;
		*) continue ;;
		esac
		if [ -e "$hp" ]; then
			real=$(/bin/realpath "$hp" 2>/dev/null) || return 1
			printf '%s\n' "$real"
			continue
		fi
		cur=$hp
		tail=""
		while [ ! -e "$cur" ] && [ ! -L "$cur" ] && [ "$cur" != / ]; do
			base=$(/usr/bin/basename "$cur")
			tail="/${base}${tail}"
			cur=$(/usr/bin/dirname "$cur")
		done
		hops=0
		while [ -L "$cur" ] && [ ! -e "$cur" ]; do
			if [ "$hops" -ge 16 ]; then
				return 1
			fi
			target=$(/usr/bin/readlink "$cur" 2>/dev/null) || return 1
			case "$target" in
			/*) cur=${target%/} ;;
			*)
				pdir=$(/usr/bin/dirname "$cur")
				if [ -e "$pdir" ] && rdir=$(/bin/realpath "$pdir" 2>/dev/null); then
					cur="${rdir%/}/${target}"
				else
					cur="${pdir%/}/${target}"
				fi
				;;
			esac
			printf '%s\n' "${cur}${tail}"
			hops=$((hops + 1))
		done
		if [ -e "$cur" ] && [ "$cur" != / ]; then
			real=$(/bin/realpath "$cur" 2>/dev/null) || return 1
			printf '%s\n' "${real%/}${tail}"
		fi
	done
}

keydir_ok() {
	if [ ! -d "$KEYDIR" ] || [ -L "$KEYDIR" ]; then
		return 1
	fi
	info=$(/usr/bin/stat -f '%u %Mp%Lp' "$KEYDIR" 2>/dev/null) || return 1
	set -- $info
	case "${1:-}" in
	'' | 0 | *[!0-9]*) return 1 ;;
	esac
	case "${2:-}" in
	0700 | 0750 | 0755) return 0 ;;
	*) return 1 ;;
	esac
}

private_key_in_shared_dir() {
	if [ ! -d "$KEYDIR" ] || [ -L "$KEYDIR" ]; then
		return 0
	fi
	if /usr/bin/find "$KEYDIR" -mindepth 1 ! -type f 2>/dev/null | /usr/bin/grep -q .; then
		return 0
	fi
	if /usr/bin/find "$KEYDIR" -mindepth 1 -maxdepth 1 -type f 2>/dev/null | /usr/bin/grep -Eiq '\.(key|priv|conf|pem)$'; then
		return 0
	fi
	if /usr/bin/find "$KEYDIR" -mindepth 1 -maxdepth 1 -type f -exec /usr/bin/grep -Eil 'private[[:space:]]*key' {} + 2>/dev/null | /usr/bin/grep -q .; then
		return 0
	fi
	if [ -f "$PRIVKEY" ] && [ ! -L "$PRIVKEY" ] && [ -s "$PRIVKEY" ]; then
		if /usr/bin/find "$KEYDIR" -mindepth 1 -maxdepth 1 -type f -exec /usr/bin/grep -Fl -f "$PRIVKEY" {} + 2>/dev/null | /usr/bin/grep -q .; then
			return 0
		fi
	fi
	return 1
}

ensure_dirs() {
	for d in "$STATE" "$DOCKER_CONFIG" "$RUNDIR" "$LOG_DIR"; do
		if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then
			log "$d is not a plain directory"
			down
		fi
		if [ -d "$d" ]; then
			if ! require_root_chain "$d"; then
				log "$d or a parent directory is not root-only"
				down
			fi
		else
			p=$(/usr/bin/dirname "$d")
			while [ ! -e "$p" ] && [ "$p" != / ]; do
				p=$(/usr/bin/dirname "$p")
			done
			if [ "$d" = "$RUNDIR" ]; then
				rp=$(CDPATH= cd -- "$p" 2>/dev/null && pwd -P 2>/dev/null) || rp=""
				if [ "$rp" != /private/var/run ] || ! require_root_chain "$p" /private/var/run/whalias; then
					log "$d parent directory is not root-only"
					down
				fi
			elif ! require_root_chain "$p"; then
				log "$d parent directory is not root-only"
				down
			fi
		fi
	done
	/usr/bin/install -d -o root -g wheel -m 755 "$STATE"
	/usr/bin/install -d -o root -g wheel -m 700 "$DOCKER_CONFIG"
	/usr/bin/install -d -o root -g wheel -m 755 "$RUNDIR"
	/usr/bin/install -d -o root -g wheel -m 755 "$LOG_DIR"
	for d in "$STATE" "$DOCKER_CONFIG" "$RUNDIR" "$LOG_DIR"; do
		if ! require_root_chain "$d"; then
			log "$d or a parent directory is not root-only"
			down
		fi
	done
	if [ "$(CDPATH= cd -- "$STATE" && pwd -P)" != /private/var/lib/whalias ] || \
	   [ "$(CDPATH= cd -- "$DOCKER_CONFIG" && pwd -P)" != /private/var/lib/whalias/docker-config ] || \
	   [ "$(CDPATH= cd -- "$RUNDIR" && pwd -P)" != /private/var/run/whalias ] || \
	   [ "$(CDPATH= cd -- "$LOG_DIR" && pwd -P)" != /private/var/log/whalias ]; then
		log "state, run, or log directory resolved to an unexpected path"
		down
	fi
	if [ "$(/usr/bin/stat -f '%Su:%Sg %Mp%Lp' "$DOCKER_CONFIG")" != "root:wheel 0700" ]; then
		log "$DOCKER_CONFIG does not have mode root:wheel 0700"
		down
	fi
}

ensure_mac_key() {
	if [ -L "$PRIVKEY" ] || { [ -e "$PRIVKEY" ] && [ ! -f "$PRIVKEY" ]; }; then
		log "$PRIVKEY is not a plain file"
		down
	fi
	if [ ! -s "$PRIVKEY" ]; then
		tmp="${PRIVKEY}.tmp"
		if ! (umask 077 && "$WG_BIN" genkey >"$tmp"); then
			/bin/rm -f "$tmp"
			log "wg genkey failed"
			down
		fi
		/bin/chmod 600 "$tmp"
		/usr/sbin/chown root:wheel "$tmp"
		/bin/mv -f "$tmp" "$PRIVKEY"
	fi
	/bin/chmod 600 "$PRIVKEY"
	/usr/sbin/chown root:wheel "$PRIVKEY"
	if [ -L "$KEYDIR/mac.pub" ] || { [ -e "$KEYDIR/mac.pub" ] && [ ! -f "$KEYDIR/mac.pub" ]; }; then
		log "$KEYDIR/mac.pub is not a plain file"
		down
	fi
	tmp="${STATE}/mac.pub.tmp"
	if ! "$WG_BIN" pubkey <"$PRIVKEY" >"$tmp"; then
		/bin/rm -f "$tmp"
		log "wg pubkey failed"
		down
	fi
	/bin/chmod 644 "$tmp"
	/usr/sbin/chown root:wheel "$tmp"
	/bin/mv -h -f "$tmp" "$KEYDIR/mac.pub"
}

wait_for_utun() {
	i=0
	while [ "$i" -lt 40 ]; do
		if [ -s "$TUN_NAME_FILE" ]; then
			iface=$(/usr/bin/head -n 1 "$TUN_NAME_FILE" | /usr/bin/tr -d '[:space:]')
			if valid_utun "$iface" && /sbin/ifconfig "$iface" >/dev/null 2>&1; then
				return 0
			fi
			iface=""
		fi
		if [ -n "$pid" ] && ! /bin/ps -p "$pid" >/dev/null 2>&1; then
			log "wireguard-go exited before naming a utun"
			down
		fi
		/bin/sleep 0.25
		i=$((i + 1))
	done
	log "wireguard-go did not name a utun"
	down
}

set_peer() {
	"$WG_BIN" set "$iface" \
		private-key "$PRIVKEY" \
		peer "$cpub" \
		allowed-ips "$ALLOWED" \
		endpoint "127.0.0.1:${uport}" \
		persistent-keepalive 25
}

run() {
	log "helper starting"
	if [ ! -x "$DOCKER_BIN" ] || [ ! -x "$WG_BIN" ] || [ ! -x "$WGGO_BIN" ]; then
		log "Docker Desktop CLI, wg, or wireguard-go is missing"
		down
	fi
	ensure_dirs
	DOCKER_HOST=$(discover_docker_host || true)
	if ! socket_url_ok "$DOCKER_HOST"; then
		log "Docker could not be inspected"
		down
	fi
	st=$(container_state || true)
	case "$st" in
	[0-9a-f]*" true /${CONTAINER}") cid=${st%" true /${CONTAINER}"} ;;
	*)
		log "controller $CONTAINER is not running"
		down
		;;
	esac
	if ! valid_cid "$cid"; then
		log "controller $CONTAINER returned an unexpected id"
		down
	fi
	if ! keydir_ok; then
		log "$KEYDIR is missing or has unexpected owner/permissions. Run setup.sh."
		down
	fi
	if ! mounts_raw=$(run_docker container inspect -f '{{.Id}} {{.State.Running}} {{.Name}}{{printf "\n"}}{{range .Mounts}}{{.Destination}}{{printf "\t"}}{{.Type}}{{printf "\t"}}{{.Source}}{{printf "\n"}}{{end}}' "$cid"); then
		log "controller $CONTAINER mounts could not be inspected"
		down
	fi
	if ! key_mounts=$(printf '%s\n' "$mounts_raw" | parse_cid_mounts "$cid true /${CONTAINER}" keys); then
		log "controller $CONTAINER changed or returned malformed mounts"
		down
	fi
	if ! printf '%s\n' "$key_mounts" | key_mount_ok; then
		log "controller $CONTAINER does not bind-mount $KEYDIR at /keys"
		down
	fi
	if ! all_srcs=$(printf '%s\n' "$mounts_raw" | parse_cid_mounts "$cid true /${CONTAINER}" sources); then
		log "controller $CONTAINER changed or returned malformed mounts"
		down
	fi
	if ! resolved_srcs=$(printf '%s\n' "$all_srcs" | resolve_host_mount_sources); then
		log "controller $CONTAINER mount sources could not be resolved"
		down
	fi
	if printf '%s\n' "$resolved_srcs" | mount_exposes_privkey; then
		log "controller $CONTAINER mounts a path that exposes $PRIVKEY"
		down
	fi
	if private_key_in_shared_dir; then
		log "public key directory contains a private key or unexpected entry"
		down
	fi
	ensure_mac_key
	if [ -L "$WG_LOG" ]; then
		log "$WG_LOG is a symlink"
		down
	fi
	/bin/rm -f "$TUN_NAME_FILE" "$IFACE_FILE"
	printf '%s wireguard-go -f utun\n' "$(/bin/date '+%Y-%m-%dT%H:%M:%S')" >>"$WG_LOG"
	WG_TUN_NAME_FILE="$TUN_NAME_FILE"
	export WG_TUN_NAME_FILE
	"$WGGO_BIN" -f utun >>"$WG_LOG" 2>&1 &
	pid=$!
	wait_for_utun
	printf '%s\n' "$iface" >"$IFACE_FILE"
	log "utun is $iface"
	/sbin/ifconfig "$iface" inet "$TUN_MAC" "$TUN_CTR" mtu 1280 up || down

	cpub=""
	uport=""
	i=0
	while [ "$i" -lt 90 ]; do
		check_container
		check_tunnel
		if [ -z "$cpub" ]; then
			cpub=$(read_controller_key || true)
		fi
		if [ -z "$uport" ]; then
			uport=$(published_udp_port || true)
		fi
		if [ -n "$cpub" ] && [ -n "$uport" ]; then
			break
		fi
		/bin/sleep 1
		i=$((i + 1))
	done
	if [ -z "$cpub" ] || [ -z "$uport" ]; then
		log "controller public key or published UDP port was not available"
		down
	fi
	set_peer || down
	/sbin/route -n delete -net "$STABLE_NET" >/dev/null 2>&1 || true
	/sbin/route -n add -net "$STABLE_NET" -interface "$iface" || down
	log "ready on $iface via 127.0.0.1:${uport}"

	while true; do
		check_container
		check_tunnel
		key=$(read_controller_key || true)
		port=$(published_udp_port || true)
		if [ -n "$key" ] && [ "$key" != "$cpub" ]; then
			log "controller public key changed"
			"$WG_BIN" set "$iface" peer "$cpub" remove || down
			cpub=$key
			if [ -n "$port" ]; then
				uport=$port
			fi
			set_peer || down
		elif [ -n "$port" ] && [ "$port" != "$uport" ]; then
			log "published UDP port changed to $port"
			uport=$port
			set_peer || down
		fi
		/bin/sleep 2
	done
}

if [ "$(/usr/bin/id -u)" -ne 0 ]; then
	printf '%s must run as root\n' "$LABEL" >&2
	exit 1
fi

umask 022
case "${1:-}" in
down) down ;;
"")
	trap 'down' EXIT TERM INT HUP
	run
	;;
*)
	printf 'usage: %s [down]\n' "$0" >&2
	exit 2
	;;
esac
