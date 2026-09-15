package admin

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// timeExceeded builds an ICMP time-exceeded packet quoting a TCP SYN from srcPort to dst:dstPort.
func timeExceeded(router [4]byte, dst [4]byte, srcPort, dstPort uint16, icmpType byte) []byte {
	outer := make([]byte, 20)
	outer[0], outer[9] = 0x45, 1
	copy(outer[12:16], router[:])
	icmp := []byte{icmpType, 0, 0, 0, 0, 0, 0, 0}
	inner := make([]byte, 20)
	inner[0], inner[9] = 0x45, 6
	copy(inner[16:20], dst[:])
	tcp := make([]byte, 8)
	binary.BigEndian.PutUint16(tcp[0:2], srcPort)
	binary.BigEndian.PutUint16(tcp[2:4], dstPort)
	pkt := append(append(append(outer, icmp...), inner...), tcp...)
	return pkt
}

func TestMatchICMPReply(t *testing.T) {
	dst := netip.MustParseAddr("1.1.1.1")
	pkt := timeExceeded([4]byte{62, 155, 244, 1}, [4]byte{1, 1, 1, 1}, 40123, 443, 11)
	if port, ok := matchICMPReply(pkt, dst, 443); !ok || port != 40123 {
		t.Fatalf("time exceeded: port=%d ok=%v", port, ok)
	}
	if port, ok := matchICMPReply(timeExceeded([4]byte{1, 1, 1, 1}, [4]byte{1, 1, 1, 1}, 40124, 443, 3), dst, 443); !ok || port != 40124 {
		t.Fatalf("unreachable: port=%d ok=%v", port, ok)
	}
	for name, bad := range map[string][]byte{
		"other destination": timeExceeded([4]byte{62, 155, 244, 1}, [4]byte{8, 8, 8, 8}, 40123, 443, 11),
		"other port":        timeExceeded([4]byte{62, 155, 244, 1}, [4]byte{1, 1, 1, 1}, 40123, 80, 11),
		"echo reply":        timeExceeded([4]byte{62, 155, 244, 1}, [4]byte{1, 1, 1, 1}, 40123, 443, 0),
		"truncated":         pkt[:30],
		"empty":             nil,
	} {
		if _, ok := matchICMPReply(bad, dst, 443); ok {
			t.Fatalf("%s should not match", name)
		}
	}
}
