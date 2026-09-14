package stuncheck

import (
	"context"
	"math"
	"net"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/loehnertz/LANdlord/internal/record"
)

// responder answers binding requests, dropping every dropEvery-th one and reporting the
// client's port shifted by portShift as the mapped address.
func responder(t *testing.T, dropEvery, portShift int) string {
	t.Helper()
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		count := 0
		for {
			n, addr, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			count++
			if dropEvery > 0 && count%dropEvery == 0 {
				continue
			}
			req := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
			if req.Decode() != nil {
				continue
			}
			resp, err := stun.Build(stun.NewTransactionIDSetter(req.TransactionID), stun.BindingSuccess,
				&stun.XORMappedAddress{IP: addr.IP, Port: addr.Port + portShift})
			if err != nil {
				continue
			}
			_, _ = pc.WriteToUDP(resp.Raw, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestBurstLossAndJitter(t *testing.T) {
	server := responder(t, 10, 0)
	c := New([]string{server}, time.Hour, 200, 500*time.Millisecond)
	c.wait = 200 * time.Millisecond
	var buf record.Buffer
	c.burstOnce(context.Background(), server, &buf)
	recs := buf.Filter(record.CSTUN, record.NBurst)
	if len(recs) != 1 {
		t.Fatalf("bursts = %d", len(recs))
	}
	v := recs[0].Values
	if v["sent"] != 100 || v["received"] != 90 || math.Abs(v["loss_pct"]-10) > 0.01 || v["rtt_p50_ms"] <= 0 {
		t.Fatalf("burst values = %v", v)
	}
	if _, ok := v["jitter_ms"]; !ok {
		t.Fatal("jitter missing")
	}
}

func TestNATDetection(t *testing.T) {
	for _, tc := range []struct {
		shift int
		want  string
	}{{0, "endpoint-independent"}, {7, "endpoint-dependent"}} {
		a := responder(t, 0, 0)
		b := responder(t, 0, tc.shift)
		c := New([]string{a, b}, time.Hour, 0, 0)
		var buf record.Buffer
		c.detectNAT(context.Background(), &buf)
		nat := buf.Filter(record.CSTUN, record.NNAT)
		if len(nat) != 1 || nat[0].Attrs["mapping"] != tc.want {
			t.Fatalf("shift %d: nat = %+v", tc.shift, nat)
		}
	}
}
