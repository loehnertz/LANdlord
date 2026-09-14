package platform

// ChannelFromFreq converts a centre frequency in MHz to its Wi-Fi channel number.
func ChannelFromFreq(mhz int) int {
	switch {
	case mhz == 2484:
		return 14
	case mhz >= 2412 && mhz <= 2472:
		return (mhz - 2407) / 5
	case mhz >= 5000 && mhz < 5925:
		return (mhz - 5000) / 5
	case mhz == 5935:
		return 2
	case mhz > 5950 && mhz <= 7125:
		return (mhz - 5950) / 5
	}
	return 0
}

// BandFromFreq returns "2.4", "5" or "6", or "" for an unknown frequency.
func BandFromFreq(mhz int) string {
	switch {
	case mhz <= 0:
		return ""
	case mhz < 3000:
		return "2.4"
	case mhz < 5925:
		return "5"
	default:
		return "6"
	}
}

// Overlaps reports whether two networks share radio spectrum.
func Overlaps(a, b BSS) bool {
	ba, bb := BandFromFreq(a.FreqMHz), BandFromFreq(b.FreqMHz)
	if ba == "" || ba != bb {
		return false
	}
	if ba == "2.4" {
		d := ChannelFromFreq(a.FreqMHz) - ChannelFromFreq(b.FreqMHz)
		return d > -5 && d < 5
	}
	aLo, aHi := spectrum(a)
	bLo, bHi := spectrum(b)
	return aLo < bHi && bLo < aHi
}

// spectrum returns the frequency range a 5 or 6 GHz network occupies, using the standard
// channel blocks for 40, 80 and 160 MHz rather than centring the width on the primary channel.
func spectrum(n BSS) (lo, hi int) {
	width := n.WidthMHz
	if width < 20 {
		width = 20
	}
	ch := ChannelFromFreq(n.FreqMHz)
	var base, origin int
	if BandFromFreq(n.FreqMHz) == "6" {
		base, origin = 1, 5950
	} else {
		origin = 5000
		switch {
		case ch < 100:
			base = 36
		case ch < 149:
			base = 100
		default:
			base = 149
		}
	}
	span := width / 20
	start := ch
	if ch >= base {
		start = base + ((ch-base)/4/span)*span*4
	}
	lo = origin + start*5 - 10
	return lo, lo + width
}

// WidthFromIEs reads the channel width in MHz from 802.11 information elements.
func WidthFromIEs(ies []byte) int {
	width := 20
	for len(ies) >= 2 {
		id, n := ies[0], int(ies[1])
		if len(ies) < 2+n {
			break
		}
		data := ies[2 : 2+n]
		ies = ies[2+n:]
		switch id {
		case 61: // HT Operation
			if len(data) >= 2 && data[1]&0x03 != 0 && data[1]&0x04 != 0 {
				width = max(width, 40)
			}
		case 192: // VHT Operation
			if len(data) >= 3 {
				switch data[0] {
				case 1:
					if data[2] != 0 && absDiff(data[2], data[1]) == 8 {
						width = max(width, 160)
					} else {
						width = max(width, 80)
					}
				case 2, 3:
					width = max(width, 160)
				}
			}
		case 255: // Element ID extension
			if len(data) >= 1 && data[0] == 36 {
				width = max(width, heWidth(data[1:]))
			}
		}
	}
	return width
}

// heWidth parses the 6 GHz Operation Information of an HE Operation element, if present.
func heWidth(d []byte) int {
	if len(d) < 6 {
		return 0
	}
	params := uint32(d[0]) | uint32(d[1])<<8 | uint32(d[2])<<16
	off := 3 + 1 + 2 // parameters, BSS colour, basic HE-MCS set
	if params&(1<<14) != 0 {
		off += 3 // VHT Operation Information
	}
	if params&(1<<15) != 0 {
		off++ // Max Co-Hosted BSSID Indicator
	}
	if params&(1<<17) == 0 || len(d) < off+5 {
		return 0
	}
	switch d[off+1] & 0x03 {
	case 1:
		return 40
	case 2:
		return 80
	case 3:
		return 160
	}
	return 20
}

func absDiff(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// SSIDFromBytes converts a length-prefixed SSID buffer to a string.
func SSIDFromBytes(b []byte, n int) string {
	return string(b[:max(0, min(n, len(b), 32))])
}
