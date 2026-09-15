package admin

import (
	"encoding/binary"
	"net/netip"
)

// matchICMPReply checks whether a raw IPv4 packet is an ICMP "time exceeded" or "destination
// unreachable" message about one of our TCP connection attempts to dst:dstPort, and returns
// that attempt's source port.
func matchICMPReply(pkt []byte, dst netip.Addr, dstPort uint16) (uint16, bool) {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return 0, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if len(pkt) < ihl+8 || pkt[9] != 1 { // protocol ICMP
		return 0, false
	}
	icmp := pkt[ihl:]
	if icmp[0] != 11 && icmp[0] != 3 {
		return 0, false
	}
	inner := icmp[8:]
	if len(inner) < 20 || inner[0]>>4 != 4 {
		return 0, false
	}
	innerIHL := int(inner[0]&0x0f) * 4
	if len(inner) < innerIHL+4 || inner[9] != 6 { // quoted protocol TCP
		return 0, false
	}
	if netip.AddrFrom4([4]byte(inner[16:20])) != dst {
		return 0, false
	}
	tcp := inner[innerIHL:]
	if binary.BigEndian.Uint16(tcp[2:4]) != dstPort {
		return 0, false
	}
	return binary.BigEndian.Uint16(tcp[0:2]), true
}
