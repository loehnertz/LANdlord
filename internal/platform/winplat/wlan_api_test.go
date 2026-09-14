//go:build windows && windowsapi

package winplat

import (
	"errors"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
)

func TestWifiAPI(t *testing.T) {
	w, err := New().OpenWifi()
	if errors.Is(err, platform.ErrUnsupported) {
		t.Skipf("no Wi-Fi: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	link, err := w.Link()
	if errors.Is(err, platform.ErrLocationDenied) {
		t.Skip("location access for desktop apps is off")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("link: %+v", link)
	if link.Connected && (link.QualityPct <= 0 || link.RxKbps == 0) {
		t.Fatalf("connected link without quality or rate: %+v", link)
	}
	if c, err := w.Counters(); err != nil {
		t.Fatalf("Counters: %v", err)
	} else {
		t.Logf("counters: %+v", c)
	}
	if err := w.Scan(); err != nil {
		t.Logf("Scan: %v", err)
	}
	time.Sleep(4 * time.Second)
	list, err := w.BSSList()
	if err != nil {
		t.Fatalf("BSSList: %v", err)
	}
	for _, b := range list {
		if b.FreqMHz < 2400 || b.RSSI > 0 {
			t.Fatalf("implausible BSS: %+v", b)
		}
	}
	t.Logf("%d networks", len(list))
}
