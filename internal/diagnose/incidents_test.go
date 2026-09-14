package diagnose

import (
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/store"
)

func verdicts(n int, bad map[int]Culprit, evidence map[int][]Evidence) []BucketVerdict {
	v := make([]BucketVerdict, n)
	for i, c := range bad {
		v[i] = BucketVerdict{Bad: true, Culprit: c, Evidence: evidence[i], Symptoms: []Evidence{{"internet_loss", "loss"}}}
	}
	return v
}

func TestIncidentsMergeAndSplit(t *testing.T) {
	s := newSession(20, nil)
	v := verdicts(20, map[int]Culprit{0: WifiSignal, 1: WifiSignal, 5: WifiSignal, 10: AccessLine, 15: AccessLine}, nil)
	incs := buildIncidents(s, v, th())
	if len(incs) != 3 {
		t.Fatalf("incidents = %d, want 3", len(incs))
	}
	if incs[0].First != 0 || incs[0].Last != 5 || !incs[0].End.Equal(t0.Add(60*time.Second)) {
		t.Fatalf("first incident = %+v", incs[0])
	}
	if incs[1].First != 10 || incs[2].First != 15 {
		t.Fatalf("split wrong: %d %d", incs[1].First, incs[2].First)
	}
	if len(incs[0].Symptoms) != 1 {
		t.Fatalf("symptoms not deduplicated: %v", incs[0].Symptoms)
	}
}

func TestIncidentMainCulpritSharesRunnersUp(t *testing.T) {
	s := newSession(10, nil)
	v := verdicts(10, map[int]Culprit{0: AccessLine, 1: WifiSignal, 2: AccessLine, 3: WifiSignal}, nil)
	inc := buildIncidents(s, v, th())[0]
	if inc.Culprit != WifiSignal {
		t.Fatalf("tie should go to WifiSignal, got %s", inc.Culprit)
	}
	if inc.Shares[WifiSignal] != 0.5 || inc.Shares[AccessLine] != 0.5 || len(inc.RunnersUp) != 1 || inc.RunnersUp[0] != AccessLine {
		t.Fatalf("shares/runners-up wrong: %+v %v", inc.Shares, inc.RunnersUp)
	}

	v = verdicts(10, map[int]Culprit{0: Unknown, 1: Unknown, 2: Unknown, 3: DNS}, nil)
	if got := buildIncidents(s, v, th())[0].Culprit; got != DNS {
		t.Fatalf("Unknown should lose to DNS, got %s", got)
	}
}

func TestIncidentConfidence(t *testing.T) {
	s := newSession(10, nil)
	high := verdicts(10, map[int]Culprit{0: WifiSignal, 1: WifiSignal}, map[int][]Evidence{
		0: {{"gateway", ""}, {"rssi", ""}},
		1: {{"gateway", ""}, {"wifi_event", ""}},
	})
	if got := buildIncidents(s, high, th())[0].Confidence; got != High {
		t.Fatalf("confidence = %s, want high", got)
	}
	medium := verdicts(10, map[int]Culprit{0: AccessLine}, map[int][]Evidence{
		0: {{"internet", ""}, {"crc", ""}, {"later_hop", ""}},
	})
	if got := buildIncidents(s, medium, th())[0].Confidence; got != Medium {
		t.Fatalf("confidence = %s, want medium", got)
	}
}

func TestMatchMarks(t *testing.T) {
	incs := []Incident{{Start: t0, End: t0.Add(30 * time.Second)}}
	marks := []store.Mark{
		{Time: t0.Add(30*time.Second + 90*time.Second), Tag: "call"},
		{Time: t0.Add(30*time.Second + 3*time.Minute)},
		{Time: t0.Add(-time.Minute)},
	}
	got := matchMarks(marks, incs, 2*time.Minute)
	if got[0].Incident != 0 || got[0].Tag != "call" || got[1].Incident != -1 || got[2].Incident != 0 {
		t.Fatalf("marks = %+v", got)
	}
}

func TestSummarize(t *testing.T) {
	r := Result{
		AwakeBuckets: 10,
		Verdicts:     verdicts(10, map[int]Culprit{0: WifiSignal, 1: WifiSignal, 2: WifiSignal, 8: DNS}, nil),
		Incidents: []Incident{
			{First: 0, Last: 1, Culprit: WifiSignal, Confidence: Medium},
			{First: 2, Last: 2, Culprit: WifiSignal, Confidence: High},
			{First: 8, Last: 8, Culprit: DNS, Confidence: High},
		},
	}
	summarize(&r)
	if r.BadBuckets != 4 || r.ProblemPct != 40 || r.Main != WifiSignal || r.Shares[WifiSignal] != 0.75 || r.MainConfidence != Medium {
		t.Fatalf("summary wrong: bad=%d pct=%v main=%s shares=%v conf=%s", r.BadBuckets, r.ProblemPct, r.Main, r.Shares, r.MainConfidence)
	}

	empty := Result{AwakeBuckets: 5, Verdicts: make([]BucketVerdict, 5)}
	summarize(&empty)
	if empty.Main != "" || empty.ProblemPct != 0 {
		t.Fatalf("empty summary wrong: %+v", empty)
	}
}
