#!/bin/sh
# Install the dev.whalias.tunnel LaunchDaemon, register wha.internal, and start
# the whalias controller. Run as root. launchd is bootstrapped only after the
# controller is running, because KeepAlive is false and a helper that finds the
# controller down exits and stays down.
set -eu

PATH=/usr/bin:/bin:/usr/sbin:/sbin
LC_ALL=C
export PATH LC_ALL
unset DOCKER_HOST DOCKER_CONTEXT DOCKER_CERT_PATH DOCKER_TLS_VERIFY

LABEL="dev.whalias.tunnel"
PLIST="/Library/LaunchDaemons/dev.whalias.tunnel.plist"
HELPER_DIR="/usr/local/libexec/whalias"
HELPER="/usr/local/libexec/whalias/helper.sh"
RESOLVER_DIR="/etc/resolver"
RESOLVER="/etc/resolver/wha.internal"
LOG_DIR="/var/log/whalias"

DOCKER_BIN="/Applications/Docker.app/Contents/Resources/bin/docker"
WG_BIN="/opt/homebrew/bin/wg"
WGGO_BIN="/opt/homebrew/bin/wireguard-go"
CONTAINER="whalias"
IMAGE="whalias:local"
NETWORK="whalias"
VOLUME="whalias-data"
DNS_PORT="5387"
WG_PORT="51820"

STATE="/var/lib/whalias"
KEYDIR="/var/lib/whalias/keys"
PRIVKEY="/var/lib/whalias/mac.key"
HOSTFILE="/var/lib/whalias/docker.host"
DOCKER_CONFIG="/var/lib/whalias/docker-config"
export DOCKER_CONFIG

DOCKER_HOST=""
DESKTOP_USER=""
DESKTOP_GROUP=""
cid=""

die() {
	printf '%s\n' "$*" >&2
	exit 1
}

valid_cid() {
	case "$1" in
	*[!0-9a-f]*) return 1 ;;
	esac
	[ "${#1}" -eq 64 ]
}

require_mode() {
	got=$(/usr/bin/stat -f '%Su:%Sg %Mp%Lp' "$1")
	if [ "$got" != "$2" ]; then
		die "$1 is ${got}, want $2"
	fi
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

# Every directory from / down to $1 must be root-owned and not group- or
# world-writable. Otherwise a non-root user could swap in a file that root runs or trusts.
require_root_chain() {
	dir=$(CDPATH= cd -- "$1" && pwd -P) || die "cannot resolve $1"
	while :; do
		info=$(/usr/bin/stat -f '%u %Mp%Lp' "$dir") || die "cannot inspect $dir"
		set -- $info
		if [ "${1:-}" != 0 ]; then
			die "$dir is not owned by root"
		fi
		if mode_is_gw_writable "${2:-}"; then
			die "$dir is writable by group or others (mode ${2:-unknown})"
		fi
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

# Reads IPv4 CIDRs, whitespace separated. Succeeds when any overlaps
# 10.254.253.0/24 or 10.254.254.0/30. IPv6 entries are skipped.
subnets_overlap_tunnel() {
	/usr/bin/awk '
		function ip2n(s,   p, j) {
			if (split(s, p, ".") != 4) return -1
			for (j = 1; j <= 4; j++) {
				if (p[j] !~ /^[0-9]+$/ || (p[j] + 0) > 255) return -1
			}
			return ((p[1] * 256 + p[2]) * 256 + p[3]) * 256 + p[4]
		}
		function overlap(a, al, b, bl,   m, d) {
			m = (al < bl) ? al : bl
			d = 2 ^ (32 - m)
			return int(a / d) == int(b / d)
		}
		BEGIN { hit = 0 }
		{
			for (i = 1; i <= NF; i++) {
				if (split($i, c, "/") != 2) continue
				if (c[2] !~ /^[0-9]+$/ || (c[2] + 0) > 32) continue
				n = ip2n(c[1])
				if (n < 0) continue
				if (overlap(n, c[2] + 0, ip2n("10.254.253.0"), 24)) hit = 1
				if (overlap(n, c[2] + 0, ip2n("10.254.254.0"), 30)) hit = 1
			}
		}
		END { exit hit ? 0 : 1 }
	'
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

find_desktop_user() {
	if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
		DESKTOP_USER=$SUDO_USER
	else
		DESKTOP_USER=$(/usr/bin/stat -f '%Su' /dev/console 2>/dev/null || true)
	fi
	case "$DESKTOP_USER" in
	'' | root) die "run setup with sudo from the account that runs Docker Desktop" ;;
	esac
	DESKTOP_GROUP=$(/usr/bin/id -gn "$DESKTOP_USER") || die "no primary group for $DESKTOP_USER"
}

discover_docker_host() {
	found=$(user_context_host "$DESKTOP_USER" || true)
	if socket_url_ok "$found"; then
		printf '%s\n' "$found"
		return 0
	fi
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
	return 1
}

docker_cmd() {
	"$DOCKER_BIN" --host "$DOCKER_HOST" "$@"
}

# The key directory is bind-mounted through Docker Desktop file sharing, which runs
# as the desktop user. That user must be able to reach it and write controller.pub.
# mac.key stays root 0600 in the parent.
ensure_dirs() {
	for d in "$STATE" "$DOCKER_CONFIG" "$KEYDIR" "$LOG_DIR" "$HELPER_DIR" "$RESOLVER_DIR"; do
		if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then
			die "$d is not a plain directory"
		fi
	done
	for d in "$STATE" "$DOCKER_CONFIG" "$LOG_DIR" "$HELPER_DIR" "$RESOLVER_DIR" /Library/LaunchDaemons; do
		if [ -d "$d" ]; then
			require_root_chain "$d"
		else
			p=$(/usr/bin/dirname "$d")
			while [ ! -e "$p" ] && [ "$p" != / ]; do
				p=$(/usr/bin/dirname "$p")
			done
			require_root_chain "$p"
		fi
	done
	/usr/bin/install -d -o root -g wheel -m 755 "$STATE"
	/usr/bin/install -d -o root -g wheel -m 700 "$DOCKER_CONFIG"
	/usr/bin/install -d -o "$DESKTOP_USER" -g "$DESKTOP_GROUP" -m 755 "$KEYDIR"
	/usr/bin/install -d -o root -g wheel -m 755 "$LOG_DIR"
	/usr/bin/install -d -o root -g wheel -m 755 "$HELPER_DIR"
	/usr/bin/install -d -o root -g wheel -m 755 "$RESOLVER_DIR"
	require_mode "$STATE" "root:wheel 0755"
	require_mode "$DOCKER_CONFIG" "root:wheel 0700"
	require_mode "$KEYDIR" "${DESKTOP_USER}:${DESKTOP_GROUP} 0755"
	require_root_chain "$STATE"
	require_root_chain "$DOCKER_CONFIG"
	require_root_chain "$LOG_DIR"
	require_root_chain "$HELPER_DIR"
	require_root_chain "$RESOLVER_DIR"
	require_root_chain /Library/LaunchDaemons
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

ensure_mac_key() {
	if [ -L "$PRIVKEY" ] || { [ -e "$PRIVKEY" ] && [ ! -f "$PRIVKEY" ]; }; then
		die "$PRIVKEY is not a plain file"
	fi
	if private_key_in_shared_dir; then
		die "public key directory $KEYDIR contains a private key or unexpected entry"
	fi
	if [ ! -s "$PRIVKEY" ]; then
		tmp="${PRIVKEY}.tmp"
		if ! (umask 077 && "$WG_BIN" genkey >"$tmp"); then
			/bin/rm -f "$tmp"
			die "wg genkey failed"
		fi
		/bin/chmod 600 "$tmp"
		/usr/sbin/chown root:wheel "$tmp"
		/bin/mv -f "$tmp" "$PRIVKEY"
	fi
	/bin/chmod 600 "$PRIVKEY"
	/usr/sbin/chown root:wheel "$PRIVKEY"
	if [ -L "$KEYDIR/mac.pub" ] || { [ -e "$KEYDIR/mac.pub" ] && [ ! -f "$KEYDIR/mac.pub" ]; }; then
		die "$KEYDIR/mac.pub is not a plain file"
	fi
	tmp="${STATE}/mac.pub.tmp"
	if ! "$WG_BIN" pubkey <"$PRIVKEY" >"$tmp"; then
		/bin/rm -f "$tmp"
		die "wg pubkey failed"
	fi
	/bin/chmod 644 "$tmp"
	/usr/sbin/chown root:wheel "$tmp"
	/bin/mv -h -f "$tmp" "$KEYDIR/mac.pub"
	require_mode "$PRIVKEY" "root:wheel 0600"
}

save_docker_host() {
	tmp=$(/usr/bin/mktemp "${STATE}/docker.host.XXXXXX")
	printf '%s\n' "$DOCKER_HOST" >"$tmp"
	/usr/bin/install -o root -g wheel -m 600 "$tmp" "$HOSTFILE"
	/bin/rm -f "$tmp"
	require_mode "$HOSTFILE" "root:wheel 0600"
}

write_resolver() {
	tmp=$(/usr/bin/mktemp "${STATE}/resolver.XXXXXX")
	printf '%s\n' 'nameserver 127.0.0.1' 'port 5387' >"$tmp"
	/usr/bin/install -o root -g wheel -m 644 "$tmp" "$RESOLVER"
	/bin/rm -f "$tmp"
	require_mode "$RESOLVER" "root:wheel 0644"
}

ensure_network() {
	if docker_cmd network inspect "$NETWORK" >/dev/null 2>&1; then
		driver=$(docker_cmd network inspect -f '{{.Driver}}' "$NETWORK")
		if [ "$driver" != "bridge" ]; then
			die "Docker network $NETWORK uses driver ${driver}, want bridge. Setup leaves it unchanged."
		fi
		internal=$(docker_cmd network inspect -f '{{.Internal}}' "$NETWORK")
		if [ "$internal" != "false" ]; then
			die "Docker network $NETWORK is internal. Setup leaves it unchanged."
		fi
	else
		docker_cmd network create "$NETWORK" >/dev/null
	fi
	subnet=$(docker_cmd network inspect -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' "$NETWORK")
	if printf '%s\n' "$subnet" | subnets_overlap_tunnel; then
		die "Docker network $NETWORK uses ${subnet}, which overlaps the tunnel. Setup leaves it unchanged."
	fi
}

ensure_volume() {
	if ! docker_cmd volume inspect "$VOLUME" >/dev/null 2>&1; then
		docker_cmd volume create "$VOLUME" >/dev/null
	fi
}

controller_shape() {
	meta_raw=$(docker_cmd container inspect -f '{{.Id}} {{.Name}}{{printf "\n"}}{{.Config.Image}}{{printf "\n"}}{{.HostConfig.Privileged}}' "$cid") || die "cannot inspect container $CONTAINER"
	meta=$(printf '%s\n' "$meta_raw" | strip_cid_header "$cid /${CONTAINER}") || die "container $CONTAINER changed during inspection. Setup leaves it unchanged."
	img=$(printf '%s\n' "$meta" | /usr/bin/sed -n '1p')
	priv=$(printf '%s\n' "$meta" | /usr/bin/sed -n '2p')
	if [ "$img" != "$IMAGE" ]; then
		die "container $CONTAINER uses image ${img}. Setup leaves it unchanged."
	fi
	if [ "$priv" != "true" ]; then
		die "container $CONTAINER is not privileged. Setup leaves it unchanged."
	fi
	nets_raw=$(docker_cmd container inspect -f '{{.Id}} {{.Name}}{{printf "\n"}}{{range $name, $conf := .NetworkSettings.Networks}}{{printf "%s\n" $name}}{{end}}' "$cid") || die "cannot inspect networks for $CONTAINER"
	nets=$(printf '%s\n' "$nets_raw" | strip_cid_header "$cid /${CONTAINER}") || die "container $CONTAINER changed during inspection. Setup leaves it unchanged."
	if ! printf '%s\n' "$nets" | /usr/bin/grep -qx "$NETWORK"; then
		die "container $CONTAINER is not on network $NETWORK. Setup leaves it unchanged."
	fi
	mounts_raw=$(docker_cmd container inspect -f '{{.Id}} {{.Name}}{{printf "\n"}}{{range .Mounts}}{{.Destination}}{{printf "\t"}}{{.Type}}{{printf "\t"}}{{.Source}}{{printf "\n"}}{{end}}' "$cid") || die "cannot inspect mounts for $CONTAINER"
	dests=$(printf '%s\n' "$mounts_raw" | parse_cid_mounts "$cid /${CONTAINER}" dests) || die "container $CONTAINER changed or returned malformed mounts. Setup leaves it unchanged."
	for dest in /keys /data /var/run/docker.sock; do
		if ! printf '%s\n' "$dests" | /usr/bin/grep -qx "$dest"; then
			die "container $CONTAINER is missing mount $dest. Setup leaves it unchanged."
		fi
	done
	key_mounts=$(printf '%s\n' "$mounts_raw" | parse_cid_mounts "$cid /${CONTAINER}" keys) || die "container $CONTAINER changed or returned malformed mounts. Setup leaves it unchanged."
	if ! printf '%s\n' "$key_mounts" | key_mount_ok; then
		die "container $CONTAINER mounts ${key_mounts:-nothing} at /keys, want bind:$KEYDIR. Setup leaves it unchanged."
	fi
	srcs=$(printf '%s\n' "$mounts_raw" | parse_cid_mounts "$cid /${CONTAINER}" sources) || die "container $CONTAINER changed or returned malformed mounts. Setup leaves it unchanged."
	resolved_srcs=$(printf '%s\n' "$srcs" | resolve_host_mount_sources) || die "container $CONTAINER mount sources could not be resolved. Setup leaves it unchanged."
	if printf '%s\n' "$resolved_srcs" | mount_exposes_privkey; then
		die "container $CONTAINER mounts a path that exposes $PRIVKEY. Setup leaves it unchanged."
	fi
}

# Docker Desktop maps the bind source /var/run/docker.sock to the daemon socket
# inside its VM. The desktop-linux socket on the Mac is a proxy and does not
# connect from inside a container.
start_controller() {
	if ! docker_cmd image inspect "$IMAGE" >/dev/null 2>&1; then
		die "image $IMAGE is not present"
	fi
	if st=$(docker_cmd container inspect -f '{{.Id}} {{.State.Running}} {{.Name}}' "$CONTAINER" 2>/dev/null); then
		case "$st" in
		[0-9a-f]*" true /${CONTAINER}")
			cid=${st%" true /${CONTAINER}"}
			state=true
			;;
		[0-9a-f]*" false /${CONTAINER}")
			cid=${st%" false /${CONTAINER}"}
			state=false
			;;
		*) die "container $CONTAINER returned an unexpected state" ;;
		esac
		valid_cid "$cid" || die "container $CONTAINER returned an unexpected id"
		controller_shape
		if [ "$state" != "true" ]; then
			docker_cmd start "$cid" >/dev/null
		fi
		return 0
	fi
	key_mount=$(CDPATH= cd -- "$KEYDIR" && pwd -P)
	# KeepAlive on the helper is false, so this container is not given a restart policy.
	out=$(docker_cmd run -d \
		--name "$CONTAINER" \
		--network "$NETWORK" \
		--privileged \
		-v "/var/run/docker.sock:/var/run/docker.sock" \
		-v "${key_mount}:/keys" \
		-v "${VOLUME}:/data" \
		-p "127.0.0.1:${DNS_PORT}:${DNS_PORT}/tcp" \
		-p "127.0.0.1:${DNS_PORT}:${DNS_PORT}/udp" \
		-p "127.0.0.1::${WG_PORT}/udp" \
		"$IMAGE") || die "docker run failed"
	cid=$(printf '%s\n' "$out" | /usr/bin/head -n 1 | /usr/bin/tr -d '[:space:]')
	valid_cid "$cid" || die "docker run did not return a valid container id"
}

wait_running() {
	i=0
	while [ "$i" -lt 30 ]; do
		st=$(docker_cmd container inspect -f '{{.Id}} {{.State.Running}} {{.Name}}' "$CONTAINER" 2>/dev/null || true)
		if [ "$st" = "$cid true /${CONTAINER}" ]; then
			return 0
		fi
		/bin/sleep 1
		i=$((i + 1))
	done
	die "controller $CONTAINER is not running"
}

# Every published binding for the spec must match, so a stray 0.0.0.0 bind fails.
require_publish() {
	spec=$1
	want=$2
	raw=$(docker_cmd container inspect -f '{{.Id}} {{.State.Running}} {{.Name}}{{printf "\n"}}{{range index .NetworkSettings.Ports "'"$spec"'"}}{{.HostIp}}:{{.HostPort}}{{printf "\n"}}{{end}}' "$cid" 2>/dev/null || true)
	out=$(printf '%s\n' "$raw" | strip_cid_header "$cid true /${CONTAINER}" 2>/dev/null || true)
	if [ -z "$out" ] || printf '%s\n' "$out" | /usr/bin/grep -vq -- "$want"; then
		die "controller publish ${spec} is '${out:-empty}'"
	fi
	printf '%s\n' "$out"
}

install_files() {
	here=$(CDPATH= cd -- "$(/usr/bin/dirname "$0")" && pwd)
	if [ ! -f "$here/helper.sh" ] || [ ! -f "$here/dev.whalias.tunnel.plist" ]; then
		die "helper.sh and dev.whalias.tunnel.plist must sit next to setup.sh"
	fi
	/bin/sh -n "$here/helper.sh" || die "helper.sh does not parse"
	/usr/bin/plutil -lint "$here/dev.whalias.tunnel.plist" >/dev/null
	/usr/bin/install -o root -g wheel -m 755 "$here/helper.sh" "$HELPER"
	/usr/bin/install -o root -g wheel -m 644 "$here/dev.whalias.tunnel.plist" "$PLIST"
	require_mode "$HELPER" "root:wheel 0755"
	require_mode "$PLIST" "root:wheel 0644"
}

job_loaded() {
	/bin/launchctl print "system/${LABEL}" >/dev/null 2>&1
}

bootstrap_helper() {
	if job_loaded; then
		/bin/launchctl bootout "system/${LABEL}" >/dev/null 2>&1 || true
		i=0
		while [ "$i" -lt 20 ]; do
			job_loaded || break
			/bin/sleep 0.5
			i=$((i + 1))
		done
		if job_loaded; then
			die "launchd still has system/${LABEL}. Setup did not signal it."
		fi
	fi
	printf '%s\n' "macOS may show a Background Items Added notification for ${LABEL}."
	/bin/launchctl bootstrap system "$PLIST"
}

if [ "$#" -ne 0 ]; then
	printf 'usage: %s\n' "$0" >&2
	exit 2
fi
if [ "$(/usr/bin/id -u)" -ne 0 ]; then
	printf 'Run as root: sudo %s\n' "$0" >&2
	exit 1
fi
for bin in "$DOCKER_BIN" "$WG_BIN" "$WGGO_BIN"; do
	if [ ! -x "$bin" ]; then
		die "$bin is missing"
	fi
done

umask 022
find_desktop_user
DOCKER_HOST=$(discover_docker_host || true)
if ! socket_url_ok "$DOCKER_HOST"; then
	die "desktop-linux did not yield a unix Docker socket that exists"
fi

ensure_dirs
save_docker_host
ensure_mac_key
ensure_network
ensure_volume
start_controller
wait_running
require_publish "${DNS_PORT}/tcp" "^127\\.0\\.0\\.1:${DNS_PORT}$" >/dev/null
require_publish "${DNS_PORT}/udp" "^127\\.0\\.0\\.1:${DNS_PORT}$" >/dev/null
wg_map=$(require_publish "${WG_PORT}/udp" '^127\.0\.0\.1:[0-9][0-9]*$')
write_resolver
install_files
bootstrap_helper

printf '%s\n' "controller ${CONTAINER} is running"
printf '%s\n' "WireGuard publish:"
printf '%s\n' "$wg_map"
printf '%s\n' "resolver ${RESOLVER}"
printf '%s\n' "LaunchDaemon system/${LABEL}"
printf '%s\n' "helper log ${LOG_DIR}/tunnel.log"
