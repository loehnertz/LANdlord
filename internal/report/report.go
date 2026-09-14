// Package report renders a session and its diagnosis into one self-contained HTML file.
package report

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/diagnose"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/version"
)

var (
	//go:embed template.html
	templateHTML string
	//go:embed assets/app.js
	appJS string
	//go:embed assets/style.css
	styleCSS string
	//go:embed assets/uPlot.iife.min.js
	uplotJS string
	//go:embed assets/uPlot.min.css
	uplotCSS string
)

var tmpl = template.Must(template.New("report").Parse(templateHTML))

type Options struct {
	Redact      bool
	Partial     bool
	GeneratedAt time.Time
	// WlanReport is the HTML of Windows' wlanreport, embedded in the appendix when present.
	WlanReport string
	Thresholds *config.Thresholds
}

const redacted = "[redacted]"

// maxIncidentRows caps the incident table; the charts still show every incident.
const maxIncidentRows = 300

var verdictSentences = map[diagnose.Culprit]string{
	diagnose.WifiSignal:       "The Wi-Fi signal where this laptop was used is too weak. Most problems started between the laptop and the router.",
	diagnose.WifiInterference: "The Wi-Fi signal is strong enough, but the radio channel is disturbed or crowded, so data keeps having to be resent.",
	diagnose.ClientDevice:     "The Wi-Fi looked fine, yet even the router answered slowly. The laptop itself (driver, power saving or background scans) is the most likely cause.",
	diagnose.LANSaturation:    "Something else in the home filled up the internet line while the problems happened.",
	diagnose.AccessLine:       "Everything inside the home worked, but the connection broke down beyond the router: on the line, in the building's wiring or at the provider.",
	diagnose.ISPCongestion:    "The connection beyond the router gets slow in the evenings while nights are fine, which points to an overloaded provider network.",
	diagnose.IPv4Tunnel:       "Only IPv4 connections suffered while IPv6 kept working. The provider's shared IPv4 gateway (DS-Lite or CGNAT) is the likely bottleneck.",
	diagnose.DNS:              "Connections themselves worked, but looking up names through the router or provider failed or was slow.",
	diagnose.RemoteService:    "Only a single online service had problems; the home connection itself looked fine.",
	diagnose.Unknown:          "Problems were measured, but they don't match a clear pattern yet.",
}

type kv struct {
	Key, Value string
	PII        bool
}

type culpritView struct {
	Title string
	Slot  int
}

type shareView struct {
	Title, Label string
	Pct          float64
	Slot         int
}

type markView struct {
	Time, Tag, Outcome string
	Matched            bool
}

type incidentView struct {
	Start, Duration, Title, Confidence, RunnersUp string
	Slot                                          int
	Zoom                                          string
	Evidence, Symptoms                            []string
}

type speedView struct {
	Time, Down, Up, Bloat, Grade, Trigger string
}

type networkView struct {
	SSID, BSSID, Band       string
	RSSI, Channel, WidthMHz int
	Connected               bool
}

type dnsView struct {
	Label, Mean       string
	Lookups, Failures int
}

type heatCell struct {
	Level int
	Title string
	Empty bool
}

type heatRow struct {
	Day   string
	Cells []heatCell
}

type view struct {
	Partial, Redacted                       bool
	Version, Generated, Period, Recorded    string
	HasProblems, HasCause                   bool
	MainTitle, MainSentence, MainConfidence string
	MainSlot                                int
	ProblemPct                              string
	Live                                    diagnose.LiveVerdict
	Marks                                   []markView
	MarksMatched                            int
	Shares                                  []shareView
	Tenant, Landlord                        []string
	Culprits                                []culpritView
	Incidents                               []incidentView
	IncidentsHidden                         int
	Heat                                    []heatRow
	Hours                                   []string
	Findings, Unavailable                   []diagnose.Finding
	Speed                                   []speedView
	BloatGrade                              string
	Networks                                []networkView
	Router, Connection, Laptop, Settings    []kv
	DeviceLog                               string
	DNS                                     []dnsView
	WlanReport                              string
	Data, CSV                               template.HTML // base64 only, so safe to insert verbatim
	StyleCSS, UPlotCSS                      template.CSS
	UPlotJS, AppJS                          template.JS
}

// Render writes the report for a session snapshot and its diagnosis.
func Render(w io.Writer, s *aggregate.Session, r diagnose.Result, opts Options) error {
	if opts.GeneratedAt.IsZero() {
		opts.GeneratedAt = time.Now()
	}
	if opts.Redact {
		s = redactSession(s)
		opts.WlanReport = ""
	}
	sr := computeSeries(s)
	data, err := encodePayload(buildPayload(s, r, sr))
	if err != nil {
		return fmt.Errorf("encode chart data: %w", err)
	}
	var csvBuf bytes.Buffer
	if err := writeCSV(&csvBuf, s, r, sr); err != nil {
		return fmt.Errorf("encode csv: %w", err)
	}
	csvData, err := gzipBase64(csvBuf.Bytes())
	if err != nil {
		return err
	}
	v := buildView(s, r, opts)
	v.Data, v.CSV = template.HTML(data), template.HTML(csvData)
	v.StyleCSS, v.UPlotCSS = template.CSS(styleCSS), template.CSS(uplotCSS)
	v.UPlotJS, v.AppJS = template.JS(uplotJS), template.JS(appJS)
	return tmpl.Execute(w, v)
}

func buildView(s *aggregate.Session, r diagnose.Result, opts Options) view {
	loc := s.Meta.Location()
	v := view{
		Partial:     opts.Partial,
		Redacted:    opts.Redact,
		Version:     version.String(),
		Generated:   opts.GeneratedAt.In(loc).Format("Mon 2 Jan 2006, 15:04"),
		Recorded:    humanDuration(time.Duration(r.AwakeBuckets) * s.Width),
		HasProblems: r.BadBuckets > 0,
		HasCause:    r.Main != "",
		ProblemPct:  fmt.Sprintf("%.1f%%", r.ProblemPct),
		Live:        r.Live,
		BloatGrade:  r.BloatGrade,
		WlanReport:  opts.WlanReport,
	}
	end := s.Meta.End()
	if n := len(s.Buckets); !s.Meta.Finished && n > 0 {
		end = s.Buckets[n-1].Start.Add(s.Width)
	}
	v.Period = fmt.Sprintf("%s to %s (%s)", s.Meta.Start.In(loc).Format("Mon 2 Jan, 15:04"), end.In(loc).Format("Mon 2 Jan, 15:04"), utcOffset(s.Meta.TZOffsetSec))

	if v.HasCause {
		v.MainTitle = r.Main.Title()
		v.MainSentence = verdictSentences[r.Main]
		v.MainConfidence = string(r.MainConfidence)
		v.MainSlot = culpritSlots[r.Main]
	}

	for _, m := range r.Marks {
		mv := markView{Time: m.Time.In(loc).Format("Mon 2 Jan, 15:04"), Tag: m.Tag, Outcome: "The connection measured fine at that time"}
		if m.Incident >= 0 && m.Incident < len(r.Incidents) {
			mv.Matched = true
			mv.Outcome = "During a problem: " + r.Incidents[m.Incident].Culprit.Title()
			v.MarksMatched++
		}
		v.Marks = append(v.Marks, mv)
	}

	for _, c := range diagnose.AllCulprits {
		if share := r.Shares[c]; share > 0 {
			v.Shares = append(v.Shares, shareView{Title: c.Title(), Pct: share * 100, Label: fmt.Sprintf("%.0f%%", share*100), Slot: culpritSlots[c]})
		}
	}
	slices.SortStableFunc(v.Shares, func(a, b shareView) int {
		switch {
		case a.Pct > b.Pct:
			return -1
		case a.Pct < b.Pct:
			return 1
		}
		return 0
	})

	for _, rec := range r.Recommendations {
		if rec.Group == diagnose.Tenant {
			v.Tenant = append(v.Tenant, rec.Text)
		} else {
			v.Landlord = append(v.Landlord, rec.Text)
		}
	}

	seenCulprit := map[diagnose.Culprit]bool{}
	for i := len(r.Incidents) - 1; i >= 0; i-- {
		inc := r.Incidents[i]
		if !seenCulprit[inc.Culprit] {
			seenCulprit[inc.Culprit] = true
		}
		if len(v.Incidents) >= maxIncidentRows {
			v.IncidentsHidden++
			continue
		}
		iv := incidentView{
			Start:      inc.Start.In(loc).Format("Mon 2 Jan, 15:04:05"),
			Duration:   humanShort(inc.Duration()),
			Title:      inc.Culprit.Title(),
			Confidence: string(inc.Confidence),
			Slot:       culpritSlots[inc.Culprit],
			Zoom:       fmt.Sprintf("%d,%d", inc.Start.Unix()+int64(s.Meta.TZOffsetSec), inc.End.Unix()+int64(s.Meta.TZOffsetSec)),
		}
		for _, e := range inc.Evidence {
			iv.Evidence = append(iv.Evidence, e.Text)
		}
		for _, e := range inc.Symptoms {
			iv.Symptoms = append(iv.Symptoms, e.Text)
		}
		var runners []string
		for _, c := range inc.RunnersUp {
			runners = append(runners, c.Title())
		}
		iv.RunnersUp = strings.Join(runners, ", ")
		v.Incidents = append(v.Incidents, iv)
	}
	for _, c := range diagnose.AllCulprits {
		if seenCulprit[c] {
			v.Culprits = append(v.Culprits, culpritView{Title: c.Title(), Slot: culpritSlots[c]})
		}
	}

	v.Heat, v.Hours = heatmap(s, r)

	for _, f := range r.Findings {
		if strings.HasPrefix(f.ID, "unavailable:") {
			v.Unavailable = append(v.Unavailable, f)
		} else {
			v.Findings = append(v.Findings, f)
		}
	}

	for _, st := range s.SpeedTests {
		v.Speed = append(v.Speed, speedView{
			Time: st.Time.In(loc).Format("Mon 2 Jan, 15:04"), Down: fmt.Sprintf("%.1f", st.DownMbps), Up: fmt.Sprintf("%.1f", st.UpMbps),
			Bloat: fmt.Sprintf("%.0f", st.BloatMs), Grade: st.Grade, Trigger: st.Trigger,
		})
	}

	v.Networks = networks(s)
	v.Router, v.DeviceLog = routerDetails(s)
	v.Connection = connectionDetails(s)
	v.DNS = dnsDetails(s)
	v.Laptop = laptopDetails(s)
	if opts.Thresholds != nil {
		v.Settings = thresholdDetails(*opts.Thresholds)
	}
	return v
}

func heatmap(s *aggregate.Session, r diagnose.Result) ([]heatRow, []string) {
	loc := s.Meta.Location()
	type cell struct{ awake, bad int }
	var days []string
	counts := map[string]*[24]cell{}
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Asleep {
			continue
		}
		t := b.Start.In(loc)
		day := t.Format("Mon 2 Jan")
		if counts[day] == nil {
			counts[day] = &[24]cell{}
			days = append(days, day)
		}
		c := &counts[day][t.Hour()]
		c.awake++
		if i < len(r.Verdicts) && r.Verdicts[i].Bad {
			c.bad++
		}
	}
	hours := make([]string, 24)
	for h := range 24 {
		hours[h] = fmt.Sprintf("%02d", h)
	}
	rows := make([]heatRow, 0, len(days))
	for _, day := range days {
		row := heatRow{Day: day, Cells: make([]heatCell, 24)}
		for h, c := range counts[day] {
			if c.awake == 0 {
				row.Cells[h] = heatCell{Empty: true, Title: fmt.Sprintf("%s, %02d:00: not recorded", day, h)}
				continue
			}
			pct := float64(c.bad) / float64(c.awake) * 100
			row.Cells[h] = heatCell{Level: heatLevel(pct), Title: fmt.Sprintf("%s, %02d:00: problems %.0f%% of the time", day, h, pct)}
		}
		rows = append(rows, row)
	}
	return rows, hours
}

func heatLevel(pct float64) int {
	for level, limit := range []float64{0, 2, 5, 10, 25, 50} {
		if pct <= limit {
			return level
		}
	}
	return 6
}

func networks(s *aggregate.Session) []networkView {
	if len(s.Scans) == 0 {
		return nil
	}
	scan := s.Scans[len(s.Scans)-1]
	var raw []struct {
		SSID    string  `json:"ssid"`
		BSSID   string  `json:"bssid"`
		RSSI    float64 `json:"rssi"`
		Channel float64 `json:"channel"`
		Band    string  `json:"band"`
		Width   float64 `json:"width"`
	}
	if err := json.Unmarshal([]byte(scan.Attrs["networks"]), &raw); err != nil {
		return nil
	}
	connected := ""
	for i := len(s.Buckets) - 1; i >= 0; i-- {
		if b := s.Buckets[i].Wifi.BSSID; b != "" {
			connected = b
			break
		}
	}
	out := make([]networkView, 0, len(raw))
	for _, n := range raw {
		out = append(out, networkView{SSID: n.SSID, BSSID: n.BSSID, RSSI: int(n.RSSI), Channel: int(n.Channel), Band: n.Band, WidthMHz: int(n.Width),
			Connected: connected != "" && connected != redacted && strings.EqualFold(n.BSSID, connected)})
	}
	slices.SortStableFunc(out, func(a, b networkView) int { return b.RSSI - a.RSSI })
	return out
}

func routerDetails(s *aggregate.Session) ([]kv, string) {
	var out []kv
	if id, ok := s.LatestInfo(record.CRouter, record.NIdentity); ok {
		out = append(out,
			kv{Key: "Router", Value: strings.TrimSpace(id.Attrs["vendor"] + " " + id.Attrs["model"])},
			kv{Key: "Detected via", Value: id.Attrs["method"]},
			kv{Key: "Router MAC address", Value: id.Attrs["mac"], PII: true},
		)
	}
	for i := len(s.Buckets) - 1; i >= 0; i-- {
		if b := s.Buckets[i]; b.Router.Present && b.Router.LinkDownBps > 0 {
			out = append(out, kv{Key: "Line speed (router)", Value: fmt.Sprintf("%.0f Mbit/s down, %.0f Mbit/s up", b.Router.LinkDownBps/1e6, b.Router.LinkUpBps/1e6)})
			break
		}
	}
	for i := len(s.Buckets) - 1; i >= 0; i-- {
		if b := s.Buckets[i]; b.DSL.Present {
			out = append(out, kv{Key: "DSL noise margin (downstream)", Value: fmt.Sprintf("%.1f dB", b.DSL.SNRDownDB)})
			break
		}
	}
	var crc float64
	for _, b := range s.Buckets {
		crc += b.DSL.CRCDelta
	}
	if crc > 0 {
		out = append(out, kv{Key: "DSL transmission errors (CRC)", Value: fmt.Sprintf("%.0f during the recording", crc)})
	}
	log := ""
	if l, ok := s.LatestInfo(record.CFritz, record.NDeviceLog); ok {
		log = l.Attrs["log"]
	}
	return out, log
}

func connectionDetails(s *aggregate.Session) []kv {
	var out []kv
	if ip, ok := s.LatestInfo(record.CPublic, record.NPublicIP); ok {
		out = append(out, kv{Key: "Public IP address", Value: ip.Attrs["ip"], PII: true}, kv{Key: "Country", Value: ip.Attrs["loc"]})
	}
	if t, ok := s.LatestInfo(record.CUPnP, record.NTunnel); ok {
		out = append(out, kv{Key: "IPv4 tunnel", Value: t.Attrs["kind"] + " (" + t.Attrs["evidence"] + ")"})
	}
	if nat, ok := s.LatestInfo(record.CSTUN, record.NNAT); ok {
		out = append(out, kv{Key: "NAT mapping", Value: nat.Attrs["mapping"]})
	}
	minMTU := 0
	for _, p := range s.PMTU {
		if m := int(p.Values["mtu"]); m > 0 && (minMTU == 0 || m < minMTU) {
			minMTU = m
		}
	}
	if minMTU > 0 {
		out = append(out, kv{Key: "Path MTU", Value: fmt.Sprintf("%d bytes", minMTU)})
	}
	v4, v6 := false, false
	for _, b := range s.Buckets {
		for label, p := range b.Paths {
			if p.Recv > 0 {
				v4 = v4 || record.IsInetTarget(label)
				v6 = v6 || record.IsInet6Target(label)
			}
		}
		if v4 && v6 {
			break
		}
	}
	out = append(out, kv{Key: "IPv4", Value: yesNo(v4)}, kv{Key: "IPv6", Value: yesNo(v6)})
	if rt, ok := s.LatestInfo(record.CTrace, record.NRoute); ok {
		out = append(out, kv{Key: "Route to " + rt.Target, Value: strings.ReplaceAll(rt.Attrs["path"], ",", " > "), PII: true})
	}
	if rt, ok := s.LatestInfo(record.CAdmin, record.NTCPRoute); ok {
		out = append(out, kv{Key: "TCP route to " + rt.Target, Value: strings.ReplaceAll(rt.Attrs["path"], ",", " > "), PII: true})
	}
	return out
}

func dnsDetails(s *aggregate.Session) []dnsView {
	type acc struct {
		count, failed int
		sum           float64
	}
	totals := map[string]*acc{}
	for _, b := range s.Buckets {
		for label, rs := range b.DNS {
			a := totals[label]
			if a == nil {
				a = &acc{}
				totals[label] = a
			}
			a.count += rs.Count
			a.failed += rs.Failed
			a.sum += rs.MeanMs * float64(rs.Count-rs.Failed)
		}
	}
	labels := make([]string, 0, len(totals))
	for l := range totals {
		labels = append(labels, l)
	}
	slices.Sort(labels)
	out := make([]dnsView, 0, len(labels))
	for _, l := range labels {
		a := totals[l]
		mean := "-"
		if ok := a.count - a.failed; ok > 0 {
			mean = fmt.Sprintf("%.0f ms", a.sum/float64(ok))
		}
		name := l
		switch {
		case l == record.TSystem:
			name = "Windows resolver"
		case strings.HasPrefix(l, "configured:"):
			name = "Configured server " + strings.TrimPrefix(l, "configured:")
		}
		out = append(out, dnsView{Label: name, Mean: mean, Lookups: a.count, Failures: a.failed})
	}
	return out
}

func laptopDetails(s *aggregate.Session) []kv {
	var out []kv
	if a, ok := s.LatestInfo(record.CSystem, record.NAdapter); ok {
		out = append(out,
			kv{Key: "Network adapter", Value: a.Attrs["description"]},
			kv{Key: "Driver version", Value: a.Attrs["driver_version"]},
			kv{Key: "Driver date", Value: a.Attrs["driver_date"]},
			kv{Key: "Adapter MAC address", Value: a.Attrs["mac"], PII: true},
		)
	}
	if d, ok := s.LatestInfo(record.CAdmin, record.NDriver); ok {
		keys := make([]string, 0, len(d.Attrs))
		for k := range d.Attrs {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			out = append(out, kv{Key: "Driver setting: " + k, Value: d.Attrs[k]})
		}
	}
	for i := range out {
		if out[i].Value == "" {
			out[i].Value = "-"
		}
	}
	return out
}

func thresholdDetails(th config.Thresholds) []kv {
	return []kv{
		{Key: "Packet loss", Value: fmt.Sprintf("above %.1f%%", th.LossPct)},
		{Key: "Jitter", Value: fmt.Sprintf("above %.0f ms", th.JitterMs)},
		{Key: "Latency (95th percentile)", Value: fmt.Sprintf("above %.0f ms", th.RTTP95Ms)},
		{Key: "Router ping", Value: fmt.Sprintf("above %.0f ms or %.0fx the usual value", th.GatewayFloorMs, th.GatewayFactor)},
		{Key: "Weak Wi-Fi signal", Value: fmt.Sprintf("below %.0f dBm", th.RSSIWeakDBm)},
		{Key: "Wi-Fi retries", Value: fmt.Sprintf("above %.0f%%", th.RetryPct)},
		{Key: "Crowded channel", Value: fmt.Sprintf("%d or more overlapping networks", th.OverlapAPs)},
		{Key: "Full router line", Value: fmt.Sprintf("above %.0f%%", th.RouterUtil*100)},
		{Key: "Slow DNS", Value: fmt.Sprintf("above %.0f ms", th.DNSSlowMs)},
		{Key: "Problems merged when closer than", Value: th.MergeGap.String()},
	}
}

// redactSession returns a copy with network names, addresses and logs replaced.
func redactSession(src *aggregate.Session) *aggregate.Session {
	s := *src
	piiKeys := map[string]bool{"ip": true, "external_ip": true, "ssid": true, "bssid": true, "mac": true, "networks": true, "path": true, "log": true}
	scrub := func(recs []record.Record) []record.Record {
		out := make([]record.Record, len(recs))
		for i, r := range recs {
			if len(r.Attrs) > 0 {
				attrs := make(map[string]string, len(r.Attrs))
				for k, val := range r.Attrs {
					if piiKeys[k] {
						val = redacted
					}
					attrs[k] = val
				}
				r.Attrs = attrs
			}
			out[i] = r
		}
		return out
	}
	s.Infos = scrub(src.Infos)
	s.Scans = scrub(src.Scans)
	s.Buckets = make([]aggregate.Bucket, len(src.Buckets))
	for i, b := range src.Buckets {
		if b.Wifi.SSID != "" {
			b.Wifi.SSID = redacted
		}
		if b.Wifi.BSSID != "" {
			b.Wifi.BSSID = redacted
		}
		s.Buckets[i] = b
	}
	return &s
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
	case d < 48*time.Hour:
		h := d.Round(time.Minute)
		return fmt.Sprintf("%d h %02d min", int(h/time.Hour), int(h%time.Hour/time.Minute))
	default:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	}
}

func humanShort(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	return fmt.Sprintf("%d min %02d s", int(d/time.Minute), int(d%time.Minute/time.Second))
}

func utcOffset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, sec/3600, sec%3600/60)
}

func yesNo(b bool) string {
	if b {
		return "working"
	}
	return "not seen"
}
