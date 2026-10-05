#!/usr/bin/env bash
# Run the chart's squid and nginx configuration in containers and check who
# they serve. Two Docker networks stand in for the PXE subnet (the chart's
# dnsmasq.subnet) and for any other network the node is on; squid and nginx
# are attached to both, as host-network pods see every node interface.
#
#   squid: a PXE client may fetch from an ordinary web server; it may not
#          fetch from the proxy host itself (127.0.0.1), link-local addresses,
#          squid.blockedDestinationCIDRs or a port outside 80/443/nginx.port,
#          nor CONNECT to anything but 443. A client elsewhere gets nothing.
#   nginx: /static/ and /dynamic/ answer the PXE subnet and refuse others.
#
# Needs docker; run it with `make subnet-access-test` (pinned helm and yq).
# Usage: HELM=<helm> YQ=<yq> hack/subnet-access-test.sh
set -euo pipefail
cd "$(dirname "$0")/.."

HELM=${HELM:-helm}
YQ=${YQ:-yq}
prefix=isoboot-access-test
pxe_net=$prefix-pxe
other_net=$prefix-other
pxe_subnet=172.31.250.0/24
other_subnet=172.31.251.0/24
squid_pxe=172.31.250.10 squid_other=172.31.251.10
nginx_pxe=172.31.250.11 nginx_other=172.31.251.11
web_pxe=172.31.250.20 web_other=172.31.251.20
alpine_version=$(cat .alpine-version)
kube_version=$(sed -n 's/^K3S_VERSION="\(v[0-9.]*\)+.*/\1/p' test/e2e/provision/lib.sh)
work=

cleanup() {
  docker rm -f "$prefix-squid" "$prefix-nginx" "$prefix-web-pxe" "$prefix-web-other" \
    "$prefix-web-squid-host" "$prefix-client-pxe" "$prefix-client-other" >/dev/null 2>&1 || true
  docker network rm "$pxe_net" "$other_net" >/dev/null 2>&1 || true
  [ -z "$work" ] || rm -rf "$work"
}
trap cleanup EXIT
cleanup
work=$(mktemp -d)

failures=0
# expect <description> <expected> <actual>
expect() {
  if [ "$2" = "$3" ]; then
    echo "ok   $1"
  else
    echo "FAIL $1 (expected $2, got $3)" >&2
    failures=$((failures + 1))
  fi
}

# The chart's configuration, with the PXE network as dnsmasq.subnet and the
# other network as a blocked destination (as if it were the cluster's).
rendered=$("$HELM" template rel charts/isoboot --kube-version "$kube_version" \
  --set nodeName=node1 --set "dnsmasq.subnet=$pxe_subnet" \
  --set "squid.blockedDestinationCIDRs={$other_subnet}" --set nginx.port=8080 --set squid.port=3128)
config() { "$YQ" eval --no-doc "select(.kind == \"ConfigMap\" and .metadata.name == \"rel-isoboot-$1\") | .data[\"$2\"]" - <<<"$rendered"; }
config squid squid.conf >"$work/squid.conf"
config nginx-config nginx.conf \
  | sed 's|__DNS_RESOLVER__|127.0.0.11|; s|__CLUSTER_DOMAIN__|cluster.local|' >"$work/nginx.conf"
nginx_image=$("$YQ" eval '.nginx.image.repository + ":" + .nginx.image.tag' charts/isoboot/values.yaml)
mkdir -p "$work/static"
echo '#!ipxe' >"$work/static/boot.ipxe"
chmod -R a+rX "$work"

echo "Building the squid image and a curl client (Alpine $alpine_version)"
docker build -q --build-arg "ALPINE_VERSION=$alpine_version" -t "$prefix-squid" -f Dockerfile.squid . >/dev/null
docker build -q -t "$prefix-client" - >/dev/null <<EOF
FROM alpine:$alpine_version
RUN apk add --no-cache curl
EOF
docker pull -q "$nginx_image" >/dev/null

docker network create --subnet "$pxe_subnet" "$pxe_net" >/dev/null
docker network create --subnet "$other_subnet" "$other_net" >/dev/null

# start <name> <network> <ip> <docker run arguments...>: run a container with
# a fixed address on one network.
start() {
  local name=$1 network=$2 ip=$3
  shift 3
  docker run -d --name "$name" --network "$network" --ip "$ip" "$@" >/dev/null
}

# Two plain web servers on port 8080 (one of the allowed ports), one per network.
start "$prefix-web-pxe" "$pxe_net" "$web_pxe" "$nginx_image"
start "$prefix-web-other" "$other_net" "$web_other" "$nginx_image"

start "$prefix-squid" "$pxe_net" "$squid_pxe" -v "$work/squid.conf:/etc/squid/squid.conf:ro" \
  --entrypoint /bin/sh "$prefix-squid" -c '
    mkdir -p /run/squid /var/cache/squid /var/log/squid
    chown squid:squid /run/squid /var/cache/squid /var/log/squid
    squid -z --foreground -f /etc/squid/squid.conf && exec squid --foreground -f /etc/squid/squid.conf'
docker network connect --ip "$squid_other" "$other_net" "$prefix-squid"
# A web server on the proxy host's own loopback, on an allowed port: what a
# node-local service (the kubelet, say) is to the host-network squid.
docker run -d --name "$prefix-web-squid-host" --network "container:$prefix-squid" "$nginx_image" >/dev/null

start "$prefix-nginx" "$pxe_net" "$nginx_pxe" -v "$work/nginx.conf:/etc/nginx/nginx.conf:ro" \
  -v "$work/static:/data/isoboot/nginx/static/boot:ro" --tmpfs /var/run:mode=1777 "$nginx_image"
docker network connect --ip "$nginx_other" "$other_net" "$prefix-nginx"

start "$prefix-client-pxe" "$pxe_net" 172.31.250.100 "$prefix-client" sleep infinity
start "$prefix-client-other" "$other_net" 172.31.251.100 "$prefix-client" sleep infinity

# status <client: pxe|other> <curl arguments...>: the HTTP status code, or 000
# when there is no HTTP answer.
status() {
  local client=$1
  shift
  docker exec "$prefix-client-$client" curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$@" || true
}

# connect_status <client> <proxy> <target host:port>: squid's answer to a
# CONNECT (tunnel) request.
connect_status() {
  docker exec "$prefix-client-$1" curl -s -o /dev/null -w '%{http_connect}' --max-time 10 \
    -x "$2" --proxytunnel "http://$3/" || true
}

for _ in $(seq 1 60); do
  [ "$(status pxe -x "http://$squid_pxe:3128" "http://$web_pxe:8080/")" = 200 ] && break
  sleep 1
done

echo "squid"
proxy=(-x "http://$squid_pxe:3128")
expect "PXE client fetches from a web server on an allowed port" 200 \
  "$(status pxe "${proxy[@]}" "http://$web_pxe:8080/")"
expect "PXE client cannot fetch from the proxy host itself" 403 \
  "$(status pxe "${proxy[@]}" "http://127.0.0.1:8080/")"
expect "PXE client cannot fetch from a link-local address" 403 \
  "$(status pxe "${proxy[@]}" "http://169.254.169.254/")"
expect "PXE client cannot fetch from a blocked destination network" 403 \
  "$(status pxe "${proxy[@]}" "http://$web_other:8080/")"
expect "PXE client cannot fetch from a port outside 80/443/nginx.port" 403 \
  "$(status pxe "${proxy[@]}" "http://$web_pxe:8081/")"
expect "PXE client cannot CONNECT to a port other than 443" 403 \
  "$(connect_status pxe "http://$squid_pxe:3128" "$web_pxe:8080")"
expect "client outside the PXE subnet is refused" 403 \
  "$(status other -x "http://$squid_other:3128" "http://$web_pxe:8080/")"

echo "nginx"
expect "PXE client fetches /static/" 200 "$(status pxe "http://$nginx_pxe:8080/static/boot.ipxe")"
dynamic=$(status pxe "http://$nginx_pxe:8080/dynamic/healthz")
if [ "$dynamic" != 403 ] && [ "$dynamic" != 000 ]; then
  echo "ok   PXE client reaches /dynamic/ (HTTP $dynamic: no httpd here)"
else
  echo "FAIL PXE client reaches /dynamic/ (got $dynamic)" >&2
  failures=$((failures + 1))
fi
expect "client outside the PXE subnet is refused /static/" 403 \
  "$(status other "http://$nginx_other:8080/static/boot.ipxe")"
expect "client outside the PXE subnet is refused /dynamic/" 403 \
  "$(status other "http://$nginx_other:8080/dynamic/healthz")"

if [ "$failures" -gt 0 ]; then
  echo "--- squid cache.log" >&2
  docker exec "$prefix-squid" cat /var/log/squid/cache.log >&2 || true
  echo "--- nginx" >&2
  docker logs "$prefix-nginx" >&2 || true
  echo "$failures access check(s) failed" >&2
  exit 1
fi
echo "All access checks passed"
