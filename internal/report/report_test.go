package report

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/diagnose"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/sim"
	"github.com/loehnertz/LANdlord/internal/store"
)

func simulated(t *testing.T, name string) (*aggregate.Session, diagnose.Result) {
	t.Helper()
	sc, _ := sim.Get(name)
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	var buf record.Buffer
	sim.Run(sc, start, time.UTC, 3, &buf)
	meta := store.Meta{Start: start, Duration: sc.Duration, Finished: true, FinishedAt: start.Add(sc.Duration), TZOffsetSec: 7200,
		Marks: []store.Mark{{Time: start.Add(30*time.Minute + 30*time.Second), Tag: "call"}, {Time: start.Add(2 * time.Hour)}}}
	s, err := aggregate.Build(meta, aggregate.DefaultWidth, func(fn func(record.Record) error) error {
		for _, r := range buf.Records() {
			if err := fn(r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, diagnose.Analyze(s, config.Default().Thresholds)
}

func render(t *testing.T, s *aggregate.Session, r diagnose.Result, opts Options) string {
	t.Helper()
	var out bytes.Buffer
	if err := Render(&out, s, r, opts); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func scriptText(t *testing.T, doc string, id string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	var found string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, a := range n.Attr {
				if a.Key == "id" && a.Val == id && n.FirstChild != nil {
					found = n.FirstChild.Data
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if found == "" {
		t.Fatalf("script #%s not found", id)
	}
	return found
}

func gunzipBase64(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRenderReport(t *testing.T) {
	s, r := simulated(t, "weak_wifi")
	th := config.Default().Thresholds
	doc := render(t, s, r, Options{GeneratedAt: time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC), Thresholds: &th, WlanReport: "<html><body>wlan</body></html>"})

	for _, want := range []string{"Weak Wi-Fi signal", "The tenant can fix this", "Call the landlord", "Your marks", "Problems by hour", "FRITZ!Box 7590", "Thresholds used", "Windows Wi-Fi report"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("report missing %q", want)
		}
	}
	if !strings.Contains(doc, `http-equiv="Content-Security-Policy" content="default-src 'none'`) {
		t.Fatal("report has no restrictive Content-Security-Policy")
	}
	external := regexp.MustCompile(`(?i)(src\s*=\s*["']?https?://|<link[^>]+href\s*=\s*["']?https?://|url\(\s*["']?https?://|@import)`)
	if m := external.FindString(doc); m != "" {
		t.Fatalf("report references an external resource: %q", m)
	}

	var p payload
	if err := json.Unmarshal(gunzipBase64(t, scriptText(t, doc, "landlord-data")), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.X) != len(s.Buckets) || len(p.Series["gw_p95"]) != len(s.Buckets) {
		t.Fatalf("payload has %d x values, want %d", len(p.X), len(s.Buckets))
	}
	if p.X[0] != s.Meta.Start.Unix()+7200 || len(p.Incidents) != len(r.Incidents) || len(p.Marks) != 2 || p.HopLabel != "hop1" {
		t.Fatalf("payload wrong: x0=%d incidents=%d marks=%d hop=%q", p.X[0], len(p.Incidents), len(p.Marks), p.HopLabel)
	}

	rows, err := csv.NewReader(bytes.NewReader(gunzipBase64(t, scriptText(t, doc, "landlord-csv")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(s.Buckets)+1 || rows[0][0] != "time" {
		t.Fatalf("embedded csv has %d rows", len(rows))
	}
}

func TestRenderRedacted(t *testing.T) {
	s, r := simulated(t, "weak_wifi")
	doc := render(t, s, r, Options{Redact: true, WlanReport: "<p>secret wlan</p>"})
	for _, secret := range []string{"FRITZ!Box 7590 XY", "3c:a6:2f:00:00:01", "203.0.113.7", "secret wlan", "neighbour"} {
		if strings.Contains(doc, secret) {
			t.Fatalf("redacted report contains %q", secret)
		}
	}
	if !strings.Contains(doc, "private details were removed") {
		t.Fatal("redaction note missing")
	}
	if !strings.Contains(doc, "Wi-Fi networks nearby") {
		t.Fatal("redaction should keep the neighbouring networks table, only without names")
	}
	if s.Infos[1].Attrs["ip"] != "203.0.113.7" {
		t.Fatal("redaction modified the original session")
	}
}

func TestShortBlipGetsNoHeadlineCulprit(t *testing.T) {
	s, _ := simulated(t, "healthy")
	r := diagnose.Result{AwakeBuckets: len(s.Buckets), BadBuckets: 1, ProblemPct: 0.05, Main: diagnose.Unknown, Shares: map[diagnose.Culprit]float64{diagnose.Unknown: 1}}
	th := config.Default().Thresholds
	doc := render(t, s, r, Options{Thresholds: &th})
	if !strings.Contains(doc, "<h1>No problems measured</h1>") || !strings.Contains(doc, "minor hiccups") {
		t.Fatal("a single short problem should not produce a culprit headline")
	}
	r.BadBuckets, r.Main = 30, diagnose.Unknown
	if doc := render(t, s, r, Options{Thresholds: &th}); !strings.Contains(doc, "<h1>No clear cause found</h1>") {
		t.Fatal("five minutes of unexplained problems should say there is no clear cause")
	}
}

func TestRenderPartialAndHealthy(t *testing.T) {
	s, r := simulated(t, "healthy")
	doc := render(t, s, r, Options{Partial: true})
	if !strings.Contains(doc, "Recording still in progress") || !strings.Contains(doc, r.Live.Headline) {
		t.Fatal("partial banner or live headline missing")
	}
}

func TestWriteCSV(t *testing.T) {
	s, r := simulated(t, "dns_trouble")
	var buf bytes.Buffer
	if err := WriteCSV(&buf, s, r); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(s.Buckets)+1 || len(rows[0]) != len(csvHeader) {
		t.Fatalf("rows=%d cols=%d", len(rows), len(rows[0]))
	}
	problems := 0
	for _, row := range rows[1:] {
		if row[len(row)-1] == "dns" {
			problems++
		}
	}
	if problems == 0 {
		t.Fatal("no dns culprit rows in csv")
	}
}
