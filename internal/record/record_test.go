package record

import (
	"encoding/json"
	"net/netip"
	"testing"
	"time"
)

func TestRecordJSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC)
	in := Metric(CPing, NEcho, TGateway, ts, map[string]float64{"rtt_ms": 3.5})
	in.Attrs = map[string]string{"ip": "192.168.178.1"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"t":"2026-09-14T18:00:00Z","c":"ping","k":"metric","n":"echo","tg":"gateway","v":{"rtt_ms":3.5},"a":{"ip":"192.168.178.1"}}`
	if string(b) != want {
		t.Fatalf("json = %s\nwant  %s", b, want)
	}
	var out Record
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Time.Equal(in.Time) || out.Values["rtt_ms"] != 3.5 || out.Target != TGateway {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestTargets(t *testing.T) {
	v4 := InetTarget(netip.MustParseAddr("1.1.1.1"))
	v6 := InetTarget(netip.MustParseAddr("2606:4700:4700::1111"))
	if v4 != "inet:1.1.1.1" || v6 != "inet6:2606:4700:4700::1111" {
		t.Fatalf("got %q %q", v4, v6)
	}
	if !IsInetTarget(v4) || IsInetTarget(v6) || !IsInet6Target(v6) || IsInet6Target(TGateway) {
		t.Fatal("target classification wrong")
	}
	if HopTarget(2) != "hop2" {
		t.Fatal("hop target wrong")
	}
}

func TestBufferFilter(t *testing.T) {
	var b Buffer
	now := time.Now()
	b.Emit(Metric(CPing, NEcho, TGateway, now, nil))
	b.Emit(Event(CWifi, NRoam, now, nil))
	b.Emit(Unavailable(CFritz, now, "no password"))
	if got := len(b.Filter(CWifi, NRoam)); got != 1 {
		t.Fatalf("Filter roam = %d, want 1", got)
	}
	u := b.Filter(CFritz, "")
	if len(u) != 1 || u[0].Kind != KindUnavailable || u[0].Attrs["reason"] != "no password" {
		t.Fatalf("unavailable record wrong: %+v", u)
	}
}
