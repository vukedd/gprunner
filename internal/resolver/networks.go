package resolver

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// a host runs a single Runner, so nothing else is entitled to this bridge.
const BrokerNetworkName = "gprunner0"

// EnsureBrokerNetwork makes the broker bridge exist and returns its gateway,
// which is the address layers are handed as MQ_ADDR and the address the
// forwarder binds.
func (r *BuildResolver) EnsureBrokerNetwork(ctx context.Context, cidr string) (string, error) {
	gateway, subnet, err := parseGateway(cidr)
	if err != nil {
		return "", err
	}

	// kraft's network list is not trustworthy so we check directly on the kernel
	held, err := interfaceSubnet(BrokerNetworkName)
	if err != nil {
		return "", err
	}

	if held != nil {
		// check if the resolved network range differs from the one passed in the config
		if !held.IP.Equal(gateway) || held.Mask.String() != subnet.Mask.String() {
			return "", fmt.Errorf("%w: %s holds %s, configured %s; remove it with 'kraft network rm "+BrokerNetworkName+"' if no layers are attached.",
				ErrBrokerNetworkMismatch, BrokerNetworkName, held.String(), cidr)
		}

		r.logger.Debug("broker bridge adopted", "name", BrokerNetworkName, "gateway", gateway)
		return gateway.String(), nil
	}

	if err := checkSubnetFree(subnet); err != nil {
		return "", err
	}

	if err := r.createBrokerNetwork(ctx, cidr); err != nil {
		return "", err
	}

	// confirmation that kraft 0 code exit actually created the network
	held, err = interfaceSubnet(BrokerNetworkName)
	if err != nil {
		return "", err
	}
	if held == nil {
		return "", fmt.Errorf("creating %s reported success but the interface is absent", BrokerNetworkName)
	}

	r.logger.Info("broker bridge created", "name", BrokerNetworkName, "gateway", gateway)
	return gateway.String(), nil
}

func parseGateway(cidr string) (net.IP, *net.IPNet, error) {
	gateway, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %q: %w", ErrBrokerSubnetInvalid, cidr, err)
	}

	if gateway.To4() == nil {
		return nil, nil, fmt.Errorf("%w: %q is not IPv4", ErrBrokerSubnetInvalid, cidr)
	}

	// the network address is not assignable, because it can't be dialed by guests
	if gateway.Equal(subnet.IP) {
		return nil, nil, fmt.Errorf("%w: %q is the network address", ErrBrokerSubnetInvalid, cidr)
	}

	if gateway.Equal(broadcastOf(subnet)) {
		return nil, nil, fmt.Errorf("%w: %q is the broadcast address", ErrBrokerSubnetInvalid, cidr)
	}

	return gateway, subnet, nil
}

func broadcastOf(subnet *net.IPNet) net.IP {
	ip := subnet.IP.To4()
	if ip == nil {
		return nil
	}

	b := make(net.IP, len(ip))
	for i := range ip {
		b[i] = ip[i] | ^subnet.Mask[i]
	}

	return b
}

// interfaceSubnet reports the IPv4 address a named link holds, or nil when the
// link does not exist.
func interfaceSubnet(name string) (*net.IPNet, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		// since the net.InterfaceByName method returns an error both when an interface
		// isn't found and when error occurs we have to make a distinction.
		links, listErr := net.Interfaces()
		if listErr != nil {
			return nil, fmt.Errorf("listing interfaces: %w", listErr)
		}
		for _, l := range links {
			if l.Name == name {
				return nil, fmt.Errorf("inspecting %s: %w", name, err)
			}
		}

		return nil, nil
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("reading addresses of %s: %w", name, err)
	}

	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if ok && n.IP.To4() != nil {
			return n, nil
		}
	}

	return nil, fmt.Errorf("%s exists but holds no IPv4 address", name)
}

// checkSubnetFree rejects a CIDR that overlaps another interface network range
func checkSubnetFree(subnet *net.IPNet) error {
	links, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("listing interfaces: %w", err)
	}

	for _, l := range links {
		if l.Name == BrokerNetworkName {
			continue
		}

		addrs, err := l.Addrs()
		if err != nil {
			continue // a link that cannot be read cannot be shown to conflict
		}

		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}

			if subnet.Contains(n.IP) || n.Contains(subnet.IP) {
				return fmt.Errorf("%w: %s already carries %s", ErrBrokerSubnetInUse, l.Name, n.String())
			}
		}
	}

	return nil
}

func (r *BuildResolver) createBrokerNetwork(ctx context.Context, cidr string) error {
	out, err := r.runNetworkCmd(ctx, "create", "-n", cidr, BrokerNetworkName)
	if err == nil {
		return nil
	}

	// kraft keeps its own record of networks, which outlives the interfaces
	// themselves across a reboot. Getting here means that record claims the
	// bridge exists while the host disagrees, so drop it and create again;
	// otherwise every start-up on a rebooted host fails
	if strings.Contains(out, "already exists") {
		r.logger.Warn("stale kraft network record, recreating", "name", BrokerNetworkName)

		if rmOut, rmErr := r.runNetworkCmd(ctx, "rm", BrokerNetworkName); rmErr != nil {
			return fmt.Errorf("removing stale network record %s: %w — %s", BrokerNetworkName, rmErr, rmOut)
		}

		if out, err = r.runNetworkCmd(ctx, "create", "-n", cidr, BrokerNetworkName); err == nil {
			return nil
		}
	}

	if strings.Contains(out, "operation not permitted") {
		return fmt.Errorf("%w: creating bridge %s — %s", ErrNetworkPermission, BrokerNetworkName, out)
	}

	return fmt.Errorf("creating bridge %s: %w — %s", BrokerNetworkName, err, out)
}

func (r *BuildResolver) runNetworkCmd(ctx context.Context, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, r.timeouts.Network)
	defer cancel()

	full := append([]string{"network"}, args...)

	out, err := exec.CommandContext(cmdCtx, "kraft", full...).CombinedOutput()
	return string(out), err
}
