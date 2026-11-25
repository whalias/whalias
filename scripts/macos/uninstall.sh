#!/bin/sh
# Bootout dev.whalias.tunnel, remove its plist and /etc/resolver/wha.internal,
# and tear down the stable route and the utun. Run as root. Docker Desktop, the
# Docker context, and the whalias container stay as they are. Every step runs even
# when an earlier one fails; the exit status reports any failure.
set -eu

PATH=/usr/bin:/bin:/usr/sbin:/sbin
LC_ALL=C
export PATH LC_ALL

LABEL="dev.whalias.tunnel"
PLIST="/Library/LaunchDaemons/dev.whalias.tunnel.plist"
HELPER="/usr/local/libexec/whalias/helper.sh"
RESOLVER="/etc/resolver/wha.internal"
STABLE_NET="10.254.253.0/24"
TUN_MAC="10.254.254.1"
WG_BIN="/opt/homebrew/bin/wg"
PRIVKEY="/var/lib/whalias/mac.key"
RUNDIR="/var/run/whalias"
IFACE_FILE="/var/run/whalias/iface"
TUN_NAME_FILE="/var/run/whalias/tun.name"

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

has_stable_route() {
	/usr/bin/awk '
		$1 == "destination:" { dest = $2 }
		$1 == "mask:" { mask = $2 }
		END { exit (dest == "10.254.253.0" && mask == "255.255.255.0") ? 0 : 1 }
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
# tunnel. Only one carrying 10.254.254.1, or keyed with this Mac's private key, is ours.
owned_utun() {
	valid_utun "$1" || return 1
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

# Return the first owned Whalias utun among the state files and the stable route.
# A stale state file naming another VPN's utun is skipped so it neither destroys
# that VPN nor hides a real Whalias utun.
pick_owned_utun() {
	route_nic=$1
	for f in "$IFACE_FILE" "$TUN_NAME_FILE"; do
		if [ -f "$f" ] && [ ! -L "$f" ]; then
			cand=$(/usr/bin/head -n 1 "$f" | /usr/bin/tr -d '[:space:]')
			if valid_utun "$cand"; then
				if owned_utun "$cand"; then
					printf '%s\n' "$cand"
					return 0
				fi
				printf '%s is not a Whalias utun. It is left alone.\n' "$cand" >&2
			fi
		fi
	done
	if valid_utun "$route_nic"; then
		if owned_utun "$route_nic"; then
			printf '%s\n' "$route_nic"
			return 0
		fi
		printf '%s is not a Whalias utun. It is left alone.\n' "$route_nic" >&2
	fi
	return 0
}

# Delete the stable route, remove the userspace socket, and destroy the utun.
teardown() {
	rc=0
	route_nic=$(iface_from_route || true)
	name=$(pick_owned_utun "$route_nic")
	/sbin/route -n delete -net "$STABLE_NET" >/dev/null 2>&1 || true
	if [ -d "$RUNDIR" ] && [ ! -L "$RUNDIR" ]; then
		/bin/rm -f "$RUNDIR"/docker.* "$RUNDIR"/ctx.*
	fi
	if valid_utun "$name"; then
		/bin/rm -f "/var/run/wireguard/${name}.sock"
		n=0
		while [ "$n" -lt 10 ]; do
			/sbin/ifconfig "$name" >/dev/null 2>&1 || break
			/sbin/ifconfig "$name" destroy >/dev/null 2>&1 || true
			/bin/sleep 0.2
			n=$((n + 1))
		done
		if /sbin/ifconfig "$name" >/dev/null 2>&1; then
			printf '%s is still present\n' "$name" >&2
			rc=1
		else
			if [ -d "$RUNDIR" ] && [ ! -L "$RUNDIR" ]; then
				/bin/rm -f "$IFACE_FILE" "$TUN_NAME_FILE"
			fi
			printf '%s is gone\n' "$name"
		fi
	else
		if [ -d "$RUNDIR" ] && [ ! -L "$RUNDIR" ]; then
			/bin/rm -f "$IFACE_FILE" "$TUN_NAME_FILE"
		fi
	fi
	if /sbin/route -n get 10.254.253.1 2>/dev/null | has_stable_route; then
		printf '%s\n' "a route for ${STABLE_NET} is still present" >&2
		rc=1
	fi
	return "$rc"
}

job_loaded() {
	/bin/launchctl print "system/${LABEL}" >/dev/null 2>&1
}

bootout_job() {
	/bin/launchctl bootout "system/${LABEL}" >/dev/null 2>&1 || true
	if [ -f "$PLIST" ]; then
		/bin/launchctl bootout system "$PLIST" >/dev/null 2>&1 || true
	fi
	i=0
	while [ "$i" -lt 20 ]; do
		job_loaded || return 0
		/bin/sleep 0.5
		i=$((i + 1))
	done
	printf '%s\n' "launchd still has system/${LABEL}. Uninstall did not signal it." >&2
	return 1
}

if [ "$#" -ne 0 ]; then
	printf 'usage: %s\n' "$0" >&2
	exit 2
fi
if [ "$(/usr/bin/id -u)" -ne 0 ]; then
	printf 'Run as root: sudo %s\n' "$0" >&2
	exit 1
fi

failed=0
bootout_job || failed=1
teardown || failed=1

/bin/rm -f "$PLIST" "$RESOLVER" "$HELPER" || failed=1
/bin/rmdir /usr/local/libexec/whalias 2>/dev/null || true

printf '%s\n' "Docker Desktop, the Docker context, and the whalias container are unchanged"
printf '%s\n' "private key remains at /var/lib/whalias/mac.key when that file exists"
if [ "$failed" -ne 0 ]; then
	printf '%s\n' "uninstall finished with errors; review messages above" >&2
	exit 1
fi
printf '%s\n' "removed system/${LABEL}, ${PLIST}, and ${RESOLVER}"
