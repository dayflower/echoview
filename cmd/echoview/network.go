package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/dayflower/echoview/internal/discover"
)

func localIPv4(value string) (netip.Addr, error) {
	if value != "" {
		address, err := netip.ParseAddr(value)
		if err != nil || !address.Is4() {
			return netip.Addr{}, fmt.Errorf("--interface must be an IPv4 address: %q", value)
		}
		return address.Unmap(), nil
	}
	probe, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.MustParseAddrPort(discover.MulticastAddress+":3610")))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("determine default IPv4 interface: %w", err)
	}
	defer probe.Close()
	address := probe.LocalAddr().(*net.UDPAddr).IP
	result, ok := netip.AddrFromSlice(address)
	if !ok || !result.Is4() {
		return netip.Addr{}, errors.New("default route did not select an IPv4 address")
	}
	return result.Unmap(), nil
}

func openECHONETConnection(address netip.Addr, dependencies cliDependencies) (*net.UDPConn, error) {
	return dependencies.listenUDP("udp4", &net.UDPAddr{IP: net.IP(address.AsSlice()), Port: discover.Port})
}
