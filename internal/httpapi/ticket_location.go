package httpapi

import (
	"net/http"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxTicketForwardedForBytes = 4 << 10
	maxTicketForwardedHops     = 32
	maxTicketRegionBytes       = 512
)

var legacyTicketTrustedProxyPrefixes = [...]netip.Prefix{
	netip.MustParsePrefix("173.245.48.0/20"),
	netip.MustParsePrefix("103.21.244.0/22"),
	netip.MustParsePrefix("103.22.200.0/22"),
	netip.MustParsePrefix("103.31.4.0/22"),
	netip.MustParsePrefix("141.101.64.0/18"),
	netip.MustParsePrefix("108.162.192.0/18"),
	netip.MustParsePrefix("190.93.240.0/20"),
	netip.MustParsePrefix("188.114.96.0/20"),
	netip.MustParsePrefix("197.234.240.0/22"),
	netip.MustParsePrefix("198.41.128.0/17"),
	netip.MustParsePrefix("162.158.0.0/15"),
	netip.MustParsePrefix("104.16.0.0/13"),
	netip.MustParsePrefix("104.24.0.0/14"),
	netip.MustParsePrefix("172.64.0.0/13"),
	netip.MustParsePrefix("131.0.72.0/22"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
}

type ticketRegionResolver interface {
	Region(string) (string, error)
}

func legacyTicketNotificationClientIP(request *http.Request) string {
	peer, ok := parseTicketPeerAddress(request.RemoteAddr)
	if !ok {
		return ""
	}
	if !legacyTicketTrustedProxy(peer) {
		return peer.String()
	}
	values := request.Header.Values("X-Forwarded-For")
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > maxTicketForwardedForBytes {
		return peer.String()
	}
	rawAddresses := strings.Split(values[0], ",")
	if len(rawAddresses) == 0 || len(rawAddresses) > maxTicketForwardedHops {
		return peer.String()
	}
	addresses := make([]netip.Addr, len(rawAddresses))
	for index, raw := range rawAddresses {
		address, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil {
			return peer.String()
		}
		addresses[index] = address
	}
	for index := len(addresses) - 1; index >= 0; index-- {
		if !legacyTicketTrustedProxy(addresses[index]) {
			return addresses[index].String()
		}
	}
	return addresses[0].String()
}

func (s *server) ticketNotificationLocation(request *http.Request) string {
	ip := legacyTicketNotificationClientIP(request)
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return "未知"
	}
	if !address.Is4() {
		return "NULL"
	}
	if s.ticketRegionResolver == nil {
		return "未知"
	}
	region, err := s.ticketRegionResolver.Region(address.String())
	region = strings.TrimSpace(region)
	if err != nil || region == "" || !utf8.ValidString(region) || len(region) > maxTicketRegionBytes || strings.IndexByte(region, 0) >= 0 {
		return "未知"
	}
	for _, character := range region {
		if unicode.IsControl(character) {
			return "未知"
		}
	}
	return region
}

func parseTicketPeerAddress(value string) (netip.Addr, bool) {
	if addressPort, err := netip.ParseAddrPort(strings.TrimSpace(value)); err == nil {
		return addressPort.Addr(), true
	}
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	return address, err == nil
}

func legacyTicketTrustedProxy(address netip.Addr) bool {
	if !address.IsValid() || !address.Is4() {
		return false
	}
	for _, prefix := range legacyTicketTrustedProxyPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
