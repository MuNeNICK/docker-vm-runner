package containerbridge

import (
	"context"
	"fmt"
	"regexp"

	"github.com/munenick/docker-vm-runner/internal/process"
)

type Runner interface {
	Run(context.Context, process.Command) (process.Result, error)
}

type Request struct {
	Interface string
	Bridge    string
}

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)

const setupScript = `
iface="$1"
bridge="$2"

if [ ! -e "/sys/class/net/$iface" ]; then
  echo "container interface $iface not found" >&2
  exit 1
fi
if [ "$iface" = "$bridge" ]; then
  echo "container interface and bridge must be different: $iface" >&2
  exit 1
fi
if [ -e "/sys/class/net/$bridge" ]; then
  if [ ! -d "/sys/class/net/$bridge/bridge" ]; then
    echo "$bridge exists but is not a Linux bridge" >&2
    exit 1
  fi
else
  ip link add name "$bridge" type bridge
fi

mtu="$(cat "/sys/class/net/$iface/mtu" 2>/dev/null || true)"
if [ -n "$mtu" ]; then
  ip link set dev "$bridge" mtu "$mtu" 2>/dev/null || true
fi

addr4="$(ip -o -4 addr show dev "$iface" scope global 2>/dev/null || true)"
addr6="$(ip -o -6 addr show dev "$iface" scope global 2>/dev/null || true)"
routes4="$(ip -4 route show dev "$iface" 2>/dev/null || true)"
routes6="$(ip -6 route show dev "$iface" 2>/dev/null || true)"

ip addr flush dev "$iface"
ip link set dev "$iface" master "$bridge"
ip link set dev "$iface" up
ip link set dev "$bridge" up

printf '%s\n%s\n' "$addr4" "$addr6" | while read -r _ _ family cidr _; do
  [ -n "$cidr" ] || continue
  case "$family" in
    inet) ip -4 addr add "$cidr" dev "$bridge" 2>/dev/null || true ;;
    inet6) ip -6 addr add "$cidr" dev "$bridge" 2>/dev/null || true ;;
  esac
done

printf '%s\n' "$routes4" | while read -r route; do
  [ -n "$route" ] || continue
  route="$(printf '%s\n' "$route" | sed "s/ dev $iface\>/ dev $bridge/")"
  ip -4 route replace $route 2>/dev/null || true
done

printf '%s\n' "$routes6" | while read -r route; do
  [ -n "$route" ] || continue
  route="$(printf '%s\n' "$route" | sed "s/ dev $iface\>/ dev $bridge/")"
  ip -6 route replace $route 2>/dev/null || true
done
`

func Command(req Request) (process.Command, error) {
	if !interfaceNamePattern.MatchString(req.Interface) {
		return process.Command{}, fmt.Errorf("invalid container interface %q", req.Interface)
	}
	if !interfaceNamePattern.MatchString(req.Bridge) {
		return process.Command{}, fmt.Errorf("invalid container bridge %q", req.Bridge)
	}
	if req.Interface == req.Bridge {
		return process.Command{}, fmt.Errorf("container interface and bridge must be different: %s", req.Interface)
	}
	return process.Command{
		Name: "sh",
		Args: []string{"-eu", "-c", setupScript, "containerbridge", req.Interface, req.Bridge},
	}, nil
}

func Ensure(ctx context.Context, runner Runner, req Request) error {
	if runner == nil {
		return fmt.Errorf("container bridge runner is nil")
	}
	command, err := Command(req)
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, command); err != nil {
		return fmt.Errorf("prepare container interface %s on bridge %s: %w", req.Interface, req.Bridge, err)
	}
	return nil
}
