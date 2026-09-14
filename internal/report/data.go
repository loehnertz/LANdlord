package report

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/diagnose"
	"github.com/loehnertz/LANdlord/internal/record"
)

// culpritSlots map culprits to fixed categorical palette slots; 0 means the neutral "other" colour.
var culpritSlots = map[diagnose.Culprit]int{
	diagnose.WifiSignal:       1,
	diagnose.AccessLine:       2,
	diagnose.LANSaturation:    3,
	diagnose.ClientDevice:     4,
	diagnose.WifiInterference: 5,
	diagnose.DNS:              6,
	diagnose.ISPCongestion:    7,
	diagnose.IPv4Tunnel:       8,
}

// series holds one value per bucket for every chart line; nil means "no data".
type series struct {
	hopLabel                       string
	gwP95, gwLoss, hopP95, hopLoss []*float64
	inetP95, inetLoss, inetJitter  []*float64
	udpLoss, udpJitter, rssi, rx   []*float64
	routerUtil, dnsMs              []*float64
}

func ptr(v float64) *float64 { return &v }

func computeSeries(s *aggregate.Session) series {
	n := len(s.Buckets)
	sr := series{
		gwP95: make([]*float64, n), gwLoss: make([]*float64, n), hopP95: make([]*float64, n), hopLoss: make([]*float64, n),
		inetP95: make([]*float64, n), inetLoss: make([]*float64, n), inetJitter: make([]*float64, n),
		udpLoss: make([]*float64, n), udpJitter: make([]*float64, n), rssi: make([]*float64, n), rx: make([]*float64, n),
		routerUtil: make([]*float64, n), dnsMs: make([]*float64, n),
	}
	sr.hopLabel = firstHop(s)
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Asleep {
			continue
		}
		gw := b.Paths[record.TGateway]
		if gw == nil {
			gw = b.Paths[record.TGateway6]
		}
		sr.gwP95[i], sr.gwLoss[i] = pathValues(gw)
		if sr.hopLabel != "" {
			sr.hopP95[i], sr.hopLoss[i] = pathValues(b.Paths[sr.hopLabel])
		}
		var loss, p95, jitter []float64
		for label, p := range b.Paths {
			if (!record.IsInetTarget(label) && !record.IsInet6Target(label)) || p.Sent == 0 {
				continue
			}
			loss = append(loss, p.LossPct)
			if p.Recv > 0 {
				p95 = append(p95, p.P95)
				jitter = append(jitter, p.Jitter)
			}
		}
		if len(loss) > 0 {
			sr.inetLoss[i] = ptr(aggregate.Median(loss))
		}
		if len(p95) > 0 {
			sr.inetP95[i], sr.inetJitter[i] = ptr(aggregate.Median(p95)), ptr(aggregate.Median(jitter))
		}
		if b.STUN.Sent > 0 {
			sr.udpLoss[i], sr.udpJitter[i] = ptr(b.STUN.LossPct), ptr(b.STUN.Jitter)
		}
		if b.Wifi.Connected {
			sr.rssi[i], sr.rx[i] = ptr(b.Wifi.RSSIMean), ptr(b.Wifi.RxMbps)
		}
		if b.Router.Present && b.Router.LinkDownBps > 0 {
			sr.routerUtil[i] = ptr(b.Router.RxBps / b.Router.LinkDownBps * 100)
		}
		if d := b.DNS[record.TSystem]; d != nil && d.Count > d.Failed {
			sr.dnsMs[i] = ptr(d.MeanMs)
		}
	}
	return sr
}

func pathValues(p *aggregate.PathStats) (p95, loss *float64) {
	if p == nil || p.Sent == 0 {
		return nil, nil
	}
	loss = ptr(p.LossPct)
	if p.Recv > 0 {
		p95 = ptr(p.P95)
	}
	return p95, loss
}

func firstHop(s *aggregate.Session) string {
	for n := 1; n <= 3; n++ {
		label := record.HopTarget(n)
		for i := range s.Buckets {
			if p := s.Buckets[i].Paths[label]; p != nil && p.Recv > 0 {
				return label
			}
		}
	}
	return ""
}

type payloadIncident struct {
	S     int64  `json:"s"`
	E     int64  `json:"e"`
	Slot  int    `json:"slot"`
	Title string `json:"title"`
	Index int    `json:"index"`
}

type payloadEvent struct {
	T     int64  `json:"t"`
	Label string `json:"label"`
}

type payloadMark struct {
	T   int64  `json:"t"`
	Tag string `json:"tag"`
}

type payload struct {
	V         int                   `json:"v"`
	X         []int64               `json:"x"`
	Width     int                   `json:"width"`
	HopLabel  string                `json:"hopLabel"`
	Series    map[string][]*float64 `json:"series"`
	Incidents []payloadIncident     `json:"incidents"`
	Events    []payloadEvent        `json:"events"`
	Marks     []payloadMark         `json:"marks"`
}

var eventLabels = map[string]string{
	record.CWifi + "/" + record.NDisconnect:   "Wi-Fi disconnected",
	record.CWifi + "/" + record.NRoam:         "Wi-Fi switched access point",
	record.CUPnP + "/" + record.NWANReconnect: "Router reconnected to the internet",
	record.CFritz + "/" + record.NResync:      "DSL line re-synchronised",
	record.CTrace + "/" + record.NRouteChange: "Route to the internet changed",
}

// buildPayload creates the chart data. Times are shifted into the session's local offset,
// so the browser can format them without knowing the time zone.
func buildPayload(s *aggregate.Session, r diagnose.Result, sr series) payload {
	off := int64(s.Meta.TZOffsetSec)
	local := func(t time.Time) int64 { return t.Unix() + off }
	p := payload{
		V:        1,
		X:        make([]int64, len(s.Buckets)),
		Width:    int(s.Width.Seconds()),
		HopLabel: sr.hopLabel,
		Series: map[string][]*float64{
			"gw_p95": sr.gwP95, "gw_loss": sr.gwLoss, "hop_p95": sr.hopP95, "hop_loss": sr.hopLoss,
			"inet_p95": sr.inetP95, "inet_loss": sr.inetLoss, "inet_jitter": sr.inetJitter,
			"udp_loss": sr.udpLoss, "udp_jitter": sr.udpJitter, "rssi": sr.rssi, "rx": sr.rx,
			"router_util": sr.routerUtil, "dns_ms": sr.dnsMs,
		},
		Incidents: []payloadIncident{},
		Events:    []payloadEvent{},
		Marks:     []payloadMark{},
	}
	for i, b := range s.Buckets {
		p.X[i] = local(b.Start)
	}
	for i, inc := range r.Incidents {
		p.Incidents = append(p.Incidents, payloadIncident{S: local(inc.Start), E: local(inc.End), Slot: culpritSlots[inc.Culprit], Title: inc.Culprit.Title(), Index: i})
	}
	for _, ev := range s.Events {
		if label, ok := eventLabels[ev.Collector+"/"+ev.Name]; ok {
			p.Events = append(p.Events, payloadEvent{T: local(ev.Time), Label: label})
		}
	}
	for _, st := range s.SpeedTests {
		p.Events = append(p.Events, payloadEvent{T: local(st.Time), Label: fmt.Sprintf("Speed test: %.0f Mbit/s down, %.0f up", st.DownMbps, st.UpMbps)})
	}
	for _, m := range s.Meta.Marks {
		p.Marks = append(p.Marks, payloadMark{T: local(m.Time), Tag: m.Tag})
	}
	return p
}

// gzipBase64 compresses b and encodes it for embedding in the HTML.
func gzipBase64(b []byte) (string, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(b); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func encodePayload(p payload) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return gzipBase64(b)
}
