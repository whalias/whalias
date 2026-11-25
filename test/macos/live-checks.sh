#!/bin/sh
# Root live checks for the macOS Whalias tunnel. One osascript dialog runs this
# whole file. It builds whalias:local, runs setup.sh, proves the five checks,
# and always runs uninstall.sh before exiting.
set -u

PATH=/usr/bin:/bin:/usr/sbin:/sbin
LC_ALL=C
export PATH LC_ALL

REPO=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
DOCKER=/Applications/Docker.app/Contents/Resources/bin/docker
console_user=$(/usr/bin/stat -f '%Su' /dev/console 2>/dev/null || true)
console_home=$(/usr/bin/dscl . -read "/Users/${console_user}" NFSHomeDirectory 2>/dev/null | /usr/bin/awk '/NFSHomeDirectory:/ { print $2; exit }')
SOCK="unix://${console_home}/.docker/run/docker.sock"
OUT=$REPO/test/macos
LOG=$OUT/live.log
RESULTS=$OUT/results.txt
SETUP=$REPO/scripts/macos/setup.sh
UNINSTALL=$REPO/scripts/macos/uninstall.sh
CA=$OUT/ca.crt
NET=whalias-live-net
HTTP_NAME=whalias-live-http
TLS_NAME=whalias-live-tls
HTTP_DNS=whalias-live-http.wha.internal
TLS_DNS=whalias-live-tls.wha.internal

overall=fail
cleaned=0
printed=0
HOME_DIR=""
DESKTOP_USER=""
created_net=0
created_http=0
created_tls=0
TUN_CREATED=""
setup_ran=0

log() {
	printf '%s %s\n' "$(/bin/date '+%Y-%m-%dT%H:%M:%S')" "$*" >>"$LOG"
	printf '%s %s\n' "$(/bin/date '+%Y-%m-%dT%H:%M:%S')" "$*"
}

say() {
	printf '%s\n' "$*" >>"$RESULTS"
	printf '%s\n' "$*" >>"$LOG"
	printf '%s\n' "$*"
}

eng() {
	# A config without credsStore. Root cannot run docker-credential-osxkeychain
	# against the login keychain. Public base images do not need it. --host
	# keeps every engine call on the Desktop socket.
	HOME="$HOME_DIR" DOCKER_CONFIG="$OUT/empty-docker-config" /usr/bin/env \
		-u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_CERT_PATH -u DOCKER_TLS_VERIFY \
		"$DOCKER" --host "$SOCK" "$@"
}

user_docker() {
	HOME="$HOME_DIR" /usr/bin/env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_CERT_PATH -u DOCKER_TLS_VERIFY -u DOCKER_CONFIG \
		"$DOCKER" "$@"
}

has_stable_route() {
	/sbin/route -n get 10.254.253.1 2>/dev/null | /usr/bin/awk '
		$1 == "destination:" { dest = $2 }
		$1 == "mask:" { mask = $2 }
		END { exit (dest == "10.254.253.0" && mask == "255.255.255.0") ? 0 : 1 }
	'
}

our_utun() {
	for nic in $(/sbin/ifconfig -l); do
		case "$nic" in
		utun*)
			if /sbin/ifconfig "$nic" 2>/dev/null | /usr/bin/awk '
				$1 == "inet" && $2 == "10.254.254.1" { found = 1 }
				END { exit found ? 0 : 1 }
			'; then
				printf '%s\n' "$nic"
				return 0
			fi
			;;
		esac
	done
	return 1
}

stable_host() {
	printf '%s\n' "$1" | /usr/bin/awk -F. '
		NF == 4 && $1 == 10 && $2 == 254 && $3 == 253 && ($4 + 0) > 0 && ($4 + 0) < 255 { ok = 1 }
		END { exit ok ? 0 : 1 }
	'
}

load1() {
	/usr/bin/uptime | /usr/bin/awk -F'load averages?: ' 'NF > 1 { split($2, a, " "); print a[1]; exit }'
}

confirm_context() {
	label=$1
	: >"$OUT/context.err"
	ctx=$(user_docker context show 2>"$OUT/context.err" | /usr/bin/head -n 1 | /usr/bin/tr -d '[:space:]')
	host=$(user_docker context inspect desktop-linux --format '{{.Endpoints.docker.Host}}' 2>>"$OUT/context.err" | /usr/bin/head -n 1 | /usr/bin/tr -d '[:space:]')
	say "CONTEXT $label show=$ctx host=$host"
	if [ "$ctx" = "desktop-linux" ] && [ "$host" = "$SOCK" ]; then
		return 0
	fi
	say "CONTEXT $label is not desktop-linux on $SOCK"
	if [ -s "$OUT/context.err" ]; then
		say "CONTEXT $label stderr:"
		/bin/cat "$OUT/context.err" | while IFS= read -r line; do say "  $line"; done
	fi
	return 1
}

remove_keychain_ca() {
	for kc in /Library/Keychains/System.keychain "$HOME_DIR/Library/Keychains/login.keychain-db"; do
		if [ -f "$kc" ] && /usr/bin/security find-certificate -a -c Whalias -p "$kc" 2>/dev/null | /usr/bin/grep -q 'BEGIN CERTIFICATE'; then
			say "removing Whalias certificate from $kc"
			/usr/bin/security delete-certificate -c Whalias "$kc" >>"$LOG" 2>&1 || say "could not remove Whalias certificate from $kc"
		fi
	done
}

append_tail() {
	file=$1
	title=$2
	if [ -f "$file" ]; then
		say "$title"
		/usr/bin/tail -n 40 "$file" | while IFS= read -r line; do say "  $line"; done
	fi
}

cleanup() {
	if [ "$cleaned" = 1 ]; then
		return 0
	fi
	cleaned=1
	set +e
	if ! /usr/bin/grep -q '^CHECK 1 RESULT:' "$RESULTS"; then
		say "CHECK 1 RESULT: DID NOT RUN"
	fi
	if ! /usr/bin/grep -q '^CHECK 2 RESULT:' "$RESULTS"; then
		say "CHECK 2 RESULT: DID NOT RUN"
	fi
	if ! /usr/bin/grep -q '^CHECK 3 RESULT:' "$RESULTS"; then
		say "CHECK 3 RESULT: DID NOT RUN"
	fi
	if ! /usr/bin/grep -q '^CHECK 4 RESULT:' "$RESULTS"; then
		say "CHECK 4 RESULT: DID NOT RUN"
	fi
	say "CLEANUP removing dev containers and running uninstall.sh"
	if [ -n "$HOME_DIR" ] && [ -x "$DOCKER" ]; then
		if [ "$created_http" = 1 ]; then
			eng stop "$HTTP_NAME" >>"$LOG" 2>&1
			eng rm "$HTTP_NAME" >>"$LOG" 2>&1
			say "removed container $HTTP_NAME"
		fi
		if [ "$created_tls" = 1 ]; then
			eng stop "$TLS_NAME" >>"$LOG" 2>&1
			eng rm "$TLS_NAME" >>"$LOG" 2>&1
			say "removed container $TLS_NAME"
		fi
		if [ "$created_net" = 1 ]; then
			eng network disconnect "$NET" whalias >>"$LOG" 2>&1
			eng network rm "$NET" >>"$LOG" 2>&1
			say "removed network $NET"
		fi
		if eng container inspect whalias >>"$LOG" 2>&1; then
			say "controller logs:"
			eng logs --tail 40 whalias 2>&1 | while IFS= read -r line; do say "  $line"; done
		fi
	fi
	append_tail /var/log/whalias/tunnel.log "helper log:"
	say "COMMAND: /bin/sh $UNINSTALL"
	/bin/sh "$UNINSTALL" >"$OUT/uninstall.out" 2>&1
	uninstall_rc=$?
	/bin/cat "$OUT/uninstall.out" | while IFS= read -r line; do say "  $line"; done
	say "uninstall exit=$uninstall_rc"

	plist=1
	resolver=1
	route_gone=1
	utun_gone=1
	if [ -e /Library/LaunchDaemons/dev.whalias.tunnel.plist ]; then
		plist=0
		say "CHECK 5 plist still present"
	else
		say "CHECK 5 /Library/LaunchDaemons/dev.whalias.tunnel.plist is absent"
	fi
	if [ -e /etc/resolver/wha.internal ]; then
		resolver=0
		say "CHECK 5 resolver still present"
	else
		say "CHECK 5 /etc/resolver/wha.internal is absent"
	fi
	say "COMMAND: /sbin/route -n get 10.254.253.1"
	/sbin/route -n get 10.254.253.1 >"$OUT/route-end.txt" 2>&1
	/bin/cat "$OUT/route-end.txt" | while IFS= read -r line; do say "  $line"; done
	if has_stable_route; then
		route_gone=0
		say "CHECK 5 stable route still present"
	else
		say "CHECK 5 stable route is gone"
	fi
	left=$(our_utun || true)
	if [ -n "$left" ]; then
		utun_gone=0
		say "CHECK 5 utun $left still has 10.254.254.1"
	else
		say "CHECK 5 no utun has 10.254.254.1"
	fi
	if [ -n "$TUN_CREATED" ]; then
		if /sbin/ifconfig "$TUN_CREATED" >/dev/null 2>&1; then
			utun_gone=0
			say "CHECK 5 created interface $TUN_CREATED is still present"
		else
			say "CHECK 5 created interface $TUN_CREATED is gone"
		fi
	fi
	if [ "$uninstall_rc" -eq 0 ] && [ "$plist" -eq 1 ] && [ "$resolver" -eq 1 ] && [ "$route_gone" -eq 1 ] && [ "$utun_gone" -eq 1 ]; then
		say "CHECK 5 RESULT: PASS"
		say "UNINSTALL FINISHED: yes"
	else
		say "CHECK 5 RESULT: FAIL"
		if [ "$uninstall_rc" -eq 0 ]; then
			say "UNINSTALL FINISHED: yes"
		else
			say "UNINSTALL FINISHED: no"
		fi
		overall=fail
	fi
	if [ -n "$HOME_DIR" ]; then
		confirm_context end || overall=fail
		remove_keychain_ca
	fi
	say "OVERALL: $overall"
	set -e
}

finish() {
	cleanup
	/bin/cat "$RESULTS"
	exit 0
}

trap cleanup EXIT INT TERM HUP

/bin/mkdir -p "$OUT/empty-docker-config"
printf '%s\n' '{}' >"$OUT/empty-docker-config/config.json"
/bin/chmod 700 "$OUT/empty-docker-config"
: >"$LOG"
: >"$RESULTS"
log "script started"
say "STATUS: live checks, expected 12 minutes, started $(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"

if [ "$(/usr/bin/id -u)" -ne 0 ]; then
	say "not root"
	finish
fi

DESKTOP_USER=$(/usr/bin/stat -f '%Su' /dev/console 2>/dev/null || true)
case "$DESKTOP_USER" in
'' | root)
	say "console user is '${DESKTOP_USER:-empty}'"
	finish
	;;
esac
HOME_DIR=$(/usr/bin/dscl . -read "/Users/$DESKTOP_USER" NFSHomeDirectory 2>/dev/null | /usr/bin/awk '{print $2; exit}')
if [ -z "$HOME_DIR" ] || [ ! -d "$HOME_DIR" ]; then
	say "no home for $DESKTOP_USER"
	finish
fi
say "desktop user $DESKTOP_USER home $HOME_DIR"
remove_keychain_ca

if ! confirm_context start; then
	finish
fi

cores=$(/usr/sbin/sysctl -n hw.ncpu)
attempt=0
while :; do
	attempt=$((attempt + 1))
	load=$(load1)
	above=$(/usr/bin/awk -v l="$load" -v c="$cores" 'BEGIN { print (l + 0 > c + 0) ? 1 : 0 }')
	say "load attempt $attempt one-minute=$load cores=$cores"
	if [ "$above" -eq 0 ]; then
		break
	fi
	if [ "$attempt" -ge 4 ]; then
		say "BLOCKED ON LOAD"
		finish
	fi
	/bin/sleep 30
done

for name in "$HTTP_NAME" "$TLS_NAME"; do
	if eng container inspect "$name" >/dev/null 2>&1; then
		say "container $name already exists; this run will not touch it"
		finish
	fi
done
if eng network inspect "$NET" >/dev/null 2>&1; then
	say "network $NET already exists; this run will not touch it"
	finish
fi

say "STATUS: building whalias:local, expected 8 minutes, started $(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
if ! eng build --progress=plain -t whalias:local "$REPO" >"$OUT/docker-build.log" 2>&1; then
	say "docker build failed, exit non-zero. Last lines:"
	/usr/bin/tail -n 40 "$OUT/docker-build.log" | while IFS= read -r line; do say "  $line"; done
	finish
fi
say "docker build whalias:local succeeded"

say "COMMAND: /bin/sh $SETUP"
if ! /bin/sh "$SETUP" >"$OUT/setup.out" 2>&1; then
	setup_ran=1
	say "setup.sh failed. Output:"
	/bin/cat "$OUT/setup.out" | while IFS= read -r line; do say "  $line"; done
	say "COMMAND: inner sudo -n -u $DESKTOP_USER docker context inspect desktop-linux"
	/usr/bin/sudo -n -u "$DESKTOP_USER" -- /usr/bin/env -u DOCKER_CONFIG -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_CERT_PATH -u DOCKER_TLS_VERIFY \
		HOME="$HOME_DIR" "$DOCKER" context inspect desktop-linux --format '{{.Endpoints.docker.Host}}' >"$OUT/inner-sudo.out" 2>"$OUT/inner-sudo.err"
	inner_rc=$?
	say "inner sudo exit=$inner_rc"
	say "inner sudo stdout: $(/bin/cat "$OUT/inner-sudo.out" 2>/dev/null)"
	say "inner sudo stderr: $(/bin/cat "$OUT/inner-sudo.err" 2>/dev/null)"
	finish
fi
setup_ran=1
say "setup.sh succeeded. Output:"
/bin/cat "$OUT/setup.out" | while IFS= read -r line; do say "  $line"; done

say "waiting for the stable route and utun"
i=0
ready=0
while [ "$i" -lt 45 ]; do
	if has_stable_route; then
		TUN_CREATED=$(our_utun || true)
		if [ -n "$TUN_CREATED" ]; then
			ready=1
			break
		fi
	fi
	/bin/sleep 2
	i=$((i + 1))
done
if [ "$ready" -ne 1 ]; then
	say "tunnel did not become ready"
	finish
fi
say "tunnel ready on $TUN_CREATED"
say "COMMAND: /sbin/route -n get 10.254.253.1"
/sbin/route -n get 10.254.253.1 >"$OUT/route-up.txt" 2>&1
/bin/cat "$OUT/route-up.txt" | while IFS= read -r line; do say "  $line"; done
say "COMMAND: /sbin/ifconfig $TUN_CREATED"
/sbin/ifconfig "$TUN_CREATED" >"$OUT/ifconfig-up.txt" 2>&1
/bin/cat "$OUT/ifconfig-up.txt" | while IFS= read -r line; do say "  $line"; done

i=0
while [ "$i" -lt 20 ]; do
	if eng cp whalias:/data/ca.crt "$CA" >>"$LOG" 2>&1; then
		break
	fi
	/bin/sleep 1
	i=$((i + 1))
done
if [ ! -s "$CA" ] || ! /usr/bin/grep -q 'BEGIN CERTIFICATE' "$CA" || /usr/bin/grep -q 'PRIVATE KEY' "$CA"; then
	say "controller CA file is missing or is not a public certificate"
	finish
fi
/bin/chmod 644 "$CA"
say "copied public CA to $CA"

if ! eng image inspect nginx:1.27-alpine >/dev/null 2>&1; then
	say "STATUS: pulling nginx:1.27-alpine, expected 3 minutes, started $(/bin/date '+%Y-%m-%dT%H:%M:%S%z')"
	if ! eng pull nginx:1.27-alpine >"$OUT/nginx-pull.log" 2>&1; then
		say "could not pull nginx:1.27-alpine"
		/usr/bin/tail -n 20 "$OUT/nginx-pull.log" | while IFS= read -r line; do say "  $line"; done
		finish
	fi
fi

/bin/cat >"$OUT/tls.cnf" <<'EOF'
[req]
distinguished_name = req_distinguished_name
x509_extensions = v3_req
prompt = no
[req_distinguished_name]
CN = whalias-live-tls.wha.internal
[v3_req]
subjectAltName = DNS:whalias-live-tls.wha.internal
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
EOF
if ! /usr/bin/openssl req -x509 -newkey rsa:2048 -sha256 -days 2 -nodes \
	-keyout "$OUT/tls.key" -out "$OUT/tls.crt" \
	-config "$OUT/tls.cnf" >>"$LOG" 2>&1; then
	say "openssl could not write the test server certificate"
	finish
fi
/bin/chmod 644 "$OUT/tls.crt" "$OUT/tls.key"

if ! eng network create "$NET" >"$OUT/network-create.out" 2>&1; then
	say "network create failed"
	/bin/cat "$OUT/network-create.out" | while IFS= read -r line; do say "  $line"; done
	finish
fi
created_net=1
say "created network $NET $(/bin/cat "$OUT/network-create.out")"
i=0
joined=0
while [ "$i" -lt 30 ]; do
	nets=$(eng container inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{printf "%s\n" $k}}{{end}}' whalias 2>>"$LOG" || true)
	if printf '%s\n' "$nets" | /usr/bin/grep -qx "$NET"; then
		joined=1
		break
	fi
	/bin/sleep 1
	i=$((i + 1))
done
if [ "$joined" -ne 1 ]; then
	say "controller did not join $NET"
	say "networks: $nets"
	finish
fi
say "controller joined $NET"

printf '%s\n' http-body-1 >"$OUT/www-index.html"
/bin/chmod 644 "$OUT/www-index.html"
/bin/cat >"$OUT/nginx-tls.conf" <<'EOF'
server {
    listen 443 ssl;
    server_name whalias-live-tls.wha.internal;
    ssl_certificate /certs/tls.crt;
    ssl_certificate_key /certs/tls.key;
    location / {
        default_type text/plain;
        return 200 "tls-own\n";
    }
}
EOF
if ! eng run -d --name "$HTTP_NAME" --network "$NET" \
	-v "$OUT/www-index.html:/usr/share/nginx/html/index.html:ro" \
	nginx:1.27-alpine >"$OUT/run-http.out" 2>&1; then
	say "http container did not start"
	/bin/cat "$OUT/run-http.out" | while IFS= read -r line; do say "  $line"; done
	finish
fi
created_http=1

if ! eng run -d --name "$TLS_NAME" --network "$NET" \
	-v "$OUT/tls.crt:/certs/tls.crt:ro" \
	-v "$OUT/tls.key:/certs/tls.key:ro" \
	-v "$OUT/nginx-tls.conf:/etc/nginx/conf.d/default.conf:ro" \
	nginx:1.27-alpine >"$OUT/run-tls.out" 2>&1; then
	say "tls container did not start"
	/bin/cat "$OUT/run-tls.out" | while IFS= read -r line; do say "  $line"; done
	finish
fi
created_tls=1

i=0
while [ "$i" -lt 20 ]; do
	if eng logs "$TLS_NAME" 2>&1 | /usr/bin/grep -q 'start worker process'; then
		break
	fi
	/bin/sleep 1
	i=$((i + 1))
done

ports=$(eng port "$HTTP_NAME" 2>&1 || true)
bindings=$(eng container inspect -f '{{json .HostConfig.PortBindings}}' "$HTTP_NAME" 2>&1 || true)
say "CHECK 1 COMMAND: $DOCKER --host $SOCK port $HTTP_NAME"
say "CHECK 1 port output: ${ports:-empty}"
say "CHECK 1 PortBindings: $bindings"

http_ok=0
ip=""
i=0
while [ "$i" -lt 30 ]; do
	if /usr/bin/curl -q -4 -fsS --max-time 8 --http1.0 -o "$OUT/check1-body" -w '%{remote_ip}' \
		"http://$HTTP_DNS/" >"$OUT/check1-ip" 2>"$OUT/check1-err"; then
		ip=$(/bin/cat "$OUT/check1-ip")
		body=$(/bin/cat "$OUT/check1-body")
		if [ "$body" = "http-body-1" ] && stable_host "$ip"; then
			http_ok=1
			break
		fi
	fi
	/bin/sleep 2
	i=$((i + 1))
done
say "CHECK 1 COMMAND: /usr/bin/curl -q -4 -fsS --max-time 8 --http1.0 -w %{remote_ip} http://$HTTP_DNS/"
say "CHECK 1 remote_ip=${ip:-none}"
say "CHECK 1 body=$(/bin/cat "$OUT/check1-body" 2>/dev/null || true)"
if [ -s "$OUT/check1-err" ]; then
	say "CHECK 1 curl stderr: $(/usr/bin/tail -n 5 "$OUT/check1-err")"
fi
say "CHECK 1 COMMAND: /usr/bin/dscacheutil -q host -a name $HTTP_DNS"
/usr/bin/dscacheutil -q host -a name "$HTTP_DNS" >"$OUT/check1-dscache" 2>&1 || true
/bin/cat "$OUT/check1-dscache" | while IFS= read -r line; do say "  $line"; done
if [ "$http_ok" -eq 1 ] && [ -z "$ports" ] && { [ "$bindings" = "null" ] || [ "$bindings" = "{}" ] || [ "$bindings" = "map[]" ]; }; then
	say "CHECK 1 RESULT: PASS"
else
	say "CHECK 1 RESULT: FAIL"
	overall=fail
fi

whalias_https=0
own_rejected=0
own_accepted=0
i=0
while [ "$i" -lt 20 ]; do
	if /usr/bin/curl -q -4 -fsSv --http1.0 --max-time 15 --cacert "$CA" -o "$OUT/check2-whalias-body" -w '%{remote_ip}' \
		--stderr "$OUT/check2-whalias-verb" \
		"https://$HTTP_DNS/" >"$OUT/check2-whalias-ip"; then
		if [ "$(/bin/cat "$OUT/check2-whalias-body" 2>/dev/null)" = "http-body-1" ] && /usr/bin/grep -q 'issuer:.*Whalias' "$OUT/check2-whalias-verb"; then
			whalias_https=1
			break
		fi
	fi
	/bin/sleep 3
	i=$((i + 1))
done
say "CHECK 2 COMMAND: /usr/bin/curl -q -4 -fsSv --http1.0 --max-time 15 --cacert $CA https://$HTTP_DNS/"
say "CHECK 2 whalias remote_ip=$(/bin/cat "$OUT/check2-whalias-ip" 2>/dev/null)"
say "CHECK 2 whalias body=$(/bin/cat "$OUT/check2-whalias-body" 2>/dev/null)"
/usr/bin/awk '/subject:|issuer:|SSL certificate problem|error:/ { print }' "$OUT/check2-whalias-verb" 2>/dev/null | while IFS= read -r line; do
	say "  $line"
done

/usr/bin/curl -q -4 -sSv --http1.0 --max-time 15 --cacert "$CA" -o "$OUT/check2-own-reject-body" \
	--stderr "$OUT/check2-own-reject-verb" "https://$TLS_DNS/" >"$OUT/check2-own-reject-ip" 2>>"$LOG"
reject_rc=$?
if [ "$reject_rc" -ne 0 ]; then
	own_rejected=1
fi
say "CHECK 2 COMMAND: /usr/bin/curl -q -4 -sSv --http1.0 --max-time 15 --cacert $CA https://$TLS_DNS/"
say "CHECK 2 container-with-443 whalias-ca exit=$reject_rc"
/usr/bin/awk '/subject:|issuer:|SSL certificate problem|error:/ { print }' "$OUT/check2-own-reject-verb" 2>/dev/null | while IFS= read -r line; do
	say "  $line"
done

if /usr/bin/curl -q -4 -fsSv --http1.0 --max-time 15 --cacert "$OUT/tls.crt" -o "$OUT/check2-own-body" -w '%{remote_ip}' \
	--stderr "$OUT/check2-own-verb" "https://$TLS_DNS/" >"$OUT/check2-own-ip"; then
	own_ip=$(/bin/cat "$OUT/check2-own-ip")
	if stable_host "$own_ip" && /usr/bin/grep -q 'whalias-live-tls.wha.internal' "$OUT/check2-own-verb"; then
		own_accepted=1
	fi
fi
say "CHECK 2 COMMAND: /usr/bin/curl -q -4 -fsSv --http1.0 --max-time 15 --cacert $OUT/tls.crt https://$TLS_DNS/"
say "CHECK 2 container-with-443 own-cert remote_ip=$(/bin/cat "$OUT/check2-own-ip" 2>/dev/null)"
/usr/bin/awk '/subject:|issuer:|SSL certificate problem|error:/ { print }' "$OUT/check2-own-verb" 2>/dev/null | while IFS= read -r line; do
	say "  $line"
done
if [ "$whalias_https" -eq 1 ] && [ "$own_rejected" -eq 1 ] && [ "$own_accepted" -eq 1 ]; then
	say "CHECK 2 RESULT: PASS"
else
	say "CHECK 2 RESULT: FAIL whalias_https=$whalias_https own_rejected=$own_rejected own_accepted=$own_accepted"
	overall=fail
fi

say "CHECK 3 recreating $HTTP_NAME"
old_ip=$ip
if ! eng stop "$HTTP_NAME" >>"$LOG" 2>&1 || ! eng rm "$HTTP_NAME" >>"$LOG" 2>&1; then
	say "CHECK 3 could not remove the first http container"
	say "CHECK 3 RESULT: FAIL"
	overall=fail
else
	printf '%s\n' http-body-2 >"$OUT/www-index.html"
	if ! eng run -d --name "$HTTP_NAME" --network "$NET" \
		-v "$OUT/www-index.html:/usr/share/nginx/html/index.html:ro" \
		nginx:1.27-alpine >>"$LOG" 2>&1; then
		say "CHECK 3 replacement container did not start"
		say "CHECK 3 RESULT: FAIL"
		overall=fail
	else
		new_ok=0
		new_ip=""
		i=0
		while [ "$i" -lt 30 ]; do
			if /usr/bin/curl -q -4 -fsS --max-time 8 --http1.0 -o "$OUT/check3-body" -w '%{remote_ip}' \
				"http://$HTTP_DNS/" >"$OUT/check3-ip" 2>"$OUT/check3-err"; then
				new_ip=$(/bin/cat "$OUT/check3-ip")
				new_body=$(/bin/cat "$OUT/check3-body")
				if [ "$new_body" = "http-body-2" ] && [ "$new_ip" = "$old_ip" ]; then
					new_ok=1
					break
				fi
			fi
			/bin/sleep 2
			i=$((i + 1))
		done
		say "CHECK 3 COMMAND: /usr/bin/curl -q -4 -fsS --max-time 8 --http1.0 -w %{remote_ip} http://$HTTP_DNS/"
		say "CHECK 3 old_ip=$old_ip new_ip=${new_ip:-none} body=$(/bin/cat "$OUT/check3-body" 2>/dev/null)"
		say "CHECK 3 COMMAND: /usr/bin/dscacheutil -q host -a name $HTTP_DNS"
		/usr/bin/dscacheutil -q host -a name "$HTTP_DNS" >"$OUT/check3-dscache" 2>&1 || true
		/bin/cat "$OUT/check3-dscache" | while IFS= read -r line; do say "  $line"; done
		if [ "$new_ok" -eq 1 ]; then
			say "CHECK 3 RESULT: PASS"
		else
			say "CHECK 3 RESULT: FAIL"
			overall=fail
		fi
	fi
fi

say "CHECK 4 COMMAND: $DOCKER --host $SOCK stop whalias"
if eng stop whalias >"$OUT/stop-controller.out" 2>&1; then
	say "CHECK 4 docker stop exit=0"
else
	say "CHECK 4 docker stop failed"
	/bin/cat "$OUT/stop-controller.out" | while IFS= read -r line; do say "  $line"; done
fi
i=0
down=0
while [ "$i" -lt 20 ]; do
	gone_utun=$(our_utun || true)
	if ! has_stable_route && [ -z "$gone_utun" ]; then
		if [ -z "$TUN_CREATED" ] || ! /sbin/ifconfig "$TUN_CREATED" >/dev/null 2>&1; then
			down=1
			break
		fi
	fi
	/bin/sleep 2
	i=$((i + 1))
done
say "COMMAND: /sbin/route -n get 10.254.253.1"
/sbin/route -n get 10.254.253.1 >"$OUT/route-stopped.txt" 2>&1
/bin/cat "$OUT/route-stopped.txt" | while IFS= read -r line; do say "  $line"; done
if [ -n "$TUN_CREATED" ]; then
	say "COMMAND: /sbin/ifconfig $TUN_CREATED"
	if /sbin/ifconfig "$TUN_CREATED" >"$OUT/ifconfig-stopped.txt" 2>&1; then
		say "  interface still present"
		/bin/cat "$OUT/ifconfig-stopped.txt" | while IFS= read -r line; do say "  $line"; done
	else
		say "  ifconfig $TUN_CREATED: no such interface"
	fi
fi
if [ "$down" -eq 1 ]; then
	say "CHECK 4 RESULT: PASS"
else
	say "CHECK 4 RESULT: FAIL"
	overall=fail
fi

if [ "$overall" != "fail" ]; then
	overall=pass
fi
finish
