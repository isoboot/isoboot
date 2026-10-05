#!/usr/bin/env bash
# Phase 3: the "site" network the VM boots on. Bridge br-pxe with the host
# (k3s node, gateway) at .1, and a separate DHCP server (dnsmasq in a docker
# container, joined to the bridge by a veth pair) at .2 that hands out
# .100-.200 with no boot options; the chart's proxyDHCP adds those.
# Usage: network.sh <row-id>
set -euo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
load_row "${1:-}"

if ! ip link show "$BRIDGE" >/dev/null 2>&1; then
  sudo ip link add "$BRIDGE" type bridge
  sudo ip addr add "$HOST_IP/24" dev "$BRIDGE"
fi
sudo ip link set "$BRIDGE" up
sudo mkdir -p /var/run/netns

sudo docker rm -f "$DHCP_CONTAINER" >/dev/null 2>&1 || true
sudo ip link del veth-ns1-br 2>/dev/null || true
sudo docker run -d --name "$DHCP_CONTAINER" \
  --cap-add=NET_ADMIN --cap-add=NET_BIND_SERVICE \
  "alpine:$ALPINE_VERSION" sleep infinity >/dev/null
sudo docker exec "$DHCP_CONTAINER" apk add --no-cache -q dnsmasq

srv_pid=$(sudo docker inspect -f '{{.State.Pid}}' "$DHCP_CONTAINER")
sudo ln -sfn "/proc/$srv_pid/ns/net" /var/run/netns/ns1
sudo ip link add veth-ns1 type veth peer name veth-ns1-br
sudo ip link set veth-ns1-br master "$BRIDGE"
sudo ip link set veth-ns1-br up
sudo ip link set veth-ns1 netns "$srv_pid"
sudo ip netns exec ns1 ip addr add "$DHCP_IP/24" dev veth-ns1
sudo ip netns exec ns1 ip link set veth-ns1 up
sudo ip netns exec ns1 ip link set lo up

sudo docker exec -d "$DHCP_CONTAINER" dnsmasq \
  --no-daemon \
  --interface=veth-ns1 \
  --bind-interfaces \
  --dhcp-range=192.168.101.100,192.168.101.200,255.255.255.0,1h \
  --dhcp-option=option:router,"$HOST_IP" \
  --dhcp-option=option:dns-server,8.8.8.8 \
  --log-dhcp \
  --log-facility=/tmp/dnsmasq.log
for i in $(seq 1 10); do
  sudo docker exec "$DHCP_CONTAINER" pgrep dnsmasq >/dev/null 2>&1 && break
  [ "$i" = 10 ] && { sudo docker logs "$DHCP_CONTAINER" || true; fail "site DHCP server did not start"; }
  sleep 1
done

# NAT for the VM's internet access.
sudo sysctl -qw net.ipv4.ip_forward=1
sudo iptables -t nat -C POSTROUTING -s "$SUBNET" ! -o "$BRIDGE" -j MASQUERADE 2>/dev/null \
  || sudo iptables -t nat -A POSTROUTING -s "$SUBNET" ! -o "$BRIDGE" -j MASQUERADE
# k3s loads br_netfilter and its FORWARD chain would drop bridged DHCP and HTTP.
sudo iptables -C FORWARD -i "$BRIDGE" -j ACCEPT 2>/dev/null || sudo iptables -I FORWARD -i "$BRIDGE" -j ACCEPT
sudo iptables -C FORWARD -o "$BRIDGE" -j ACCEPT 2>/dev/null || sudo iptables -I FORWARD -o "$BRIDGE" -j ACCEPT

# The VM's tap device; QEMU attaches to it in boot-install.sh and verify.sh.
sudo ip link del "$TAP" 2>/dev/null || true
sudo ip tuntap add dev "$TAP" mode tap
sudo ip link set "$TAP" master "$BRIDGE"
sudo ip link set "$TAP" up
pass "site network up: $BRIDGE $HOST_IP, DHCP server $DHCP_IP"
