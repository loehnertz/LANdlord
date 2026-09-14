package platform

import "testing"

func TestChannelAndBand(t *testing.T) {
	tests := []struct {
		freq    int
		channel int
		band    string
	}{
		{2412, 1, "2.4"}, {2437, 6, "2.4"}, {2472, 13, "2.4"}, {2484, 14, "2.4"},
		{5180, 36, "5"}, {5745, 149, "5"}, {5955, 1, "6"}, {5935, 2, "6"}, {0, 0, ""},
	}
	for _, tt := range tests {
		if got := ChannelFromFreq(tt.freq); got != tt.channel {
			t.Errorf("ChannelFromFreq(%d) = %d, want %d", tt.freq, got, tt.channel)
		}
		if got := BandFromFreq(tt.freq); got != tt.band {
			t.Errorf("BandFromFreq(%d) = %q, want %q", tt.freq, got, tt.band)
		}
	}
}

func TestOverlaps(t *testing.T) {
	tests := []struct {
		name string
		a, b BSS
		want bool
	}{
		{"2.4 channels 1 and 4", BSS{FreqMHz: 2412}, BSS{FreqMHz: 2427}, true},
		{"2.4 channels 1 and 6", BSS{FreqMHz: 2412}, BSS{FreqMHz: 2437}, false},
		{"5 GHz 80 MHz on 36 and 20 MHz on 48", BSS{FreqMHz: 5180, WidthMHz: 80}, BSS{FreqMHz: 5240, WidthMHz: 20}, true},
		{"5 GHz 80 MHz on 36 and 20 MHz on 52", BSS{FreqMHz: 5180, WidthMHz: 80}, BSS{FreqMHz: 5260, WidthMHz: 20}, false},
		{"5 GHz 20 MHz on 36 and 40", BSS{FreqMHz: 5180}, BSS{FreqMHz: 5200}, false},
		{"different bands", BSS{FreqMHz: 2412}, BSS{FreqMHz: 5180}, false},
		{"6 GHz 160 MHz on 1 and 20 MHz on 29", BSS{FreqMHz: 5955, WidthMHz: 160}, BSS{FreqMHz: 6095}, true},
	}
	for _, tt := range tests {
		if got := Overlaps(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: Overlaps = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestWidthFromIEs(t *testing.T) {
	ht40 := []byte{61, 22, 36, 0x07, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	vht80 := []byte{192, 5, 1, 42, 0, 0, 0}
	vht160 := []byte{192, 5, 1, 42, 50, 0, 0}
	// HE Operation with the 6 GHz Operation Information present and width 160 MHz.
	he160 := []byte{255, 12, 36, 0x00, 0x00, 0x02, 0x00, 0xfc, 0xff, 1, 0x03, 15, 0, 0x01}
	plain := []byte{0, 4, 'h', 'o', 'm', 'e', 1, 2, 0x82, 0x84}
	tests := []struct {
		name string
		ies  []byte
		want int
	}{
		{"plain", plain, 20},
		{"ht40", append(append([]byte{}, plain...), ht40...), 40},
		{"vht80", append(append([]byte{}, ht40...), vht80...), 80},
		{"vht160", vht160, 160},
		{"he 6 GHz 160", he160, 160},
		{"truncated", []byte{192, 5, 1}, 20},
	}
	for _, tt := range tests {
		if got := WidthFromIEs(tt.ies); got != tt.want {
			t.Errorf("%s: WidthFromIEs = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestSSIDFromBytes(t *testing.T) {
	if got := SSIDFromBytes([]byte("home-network...."), 12); got != "home-network" {
		t.Fatalf("SSIDFromBytes = %q", got)
	}
	if got := SSIDFromBytes([]byte("ab"), 40); got != "ab" {
		t.Fatalf("SSIDFromBytes clamps = %q", got)
	}
}
