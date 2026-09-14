package diagnose

import (
	"fmt"
	"strings"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/config"
)

type LiveState string

const (
	Collecting LiveState = "collecting"
	NoProblems LiveState = "no_problems"
	Likely     LiveState = "likely"
	Confident  LiveState = "confident"
)

// LiveVerdict is the provisional answer shown on the status page while recording continues.
type LiveVerdict struct {
	State                LiveState
	Culprit              Culprit
	Share                float64
	AwakeFor, ProblemFor time.Duration
	Incidents            int
	Headline             string
	Caveats              []string
}

const collectingHeadline = "Still collecting data. Keep using the laptop normally and press the button when something goes wrong."

func liveVerdict(s *aggregate.Session, r *Result, th config.Thresholds) LiveVerdict {
	lv := LiveVerdict{
		State:      Collecting,
		AwakeFor:   time.Duration(r.AwakeBuckets) * s.Width,
		ProblemFor: time.Duration(r.BadBuckets) * s.Width,
		Headline:   collectingHeadline,
	}
	hasCause := r.Main != "" && r.Main != Unknown
	var mainBad time.Duration
	mainIncidents := 0
	if hasCause {
		n := 0
		for _, v := range r.Verdicts {
			if v.Bad && v.Culprit == r.Main {
				n++
			}
		}
		mainBad = time.Duration(n) * s.Width
		for _, inc := range r.Incidents {
			if inc.Culprit == r.Main {
				mainIncidents++
			}
		}
	}
	share := r.Shares[r.Main]

	switch {
	case hasCause && lv.AwakeFor >= th.ConfidentMinAwake.Duration && mainBad >= th.ConfidentMinBad.Duration &&
		share >= th.ConfidentMinShare && mainIncidents >= th.ConfidentMinIncidents && r.MainConfidence != Low:
		lv.State = Confident
		lv.Headline = fmt.Sprintf("Most likely cause: %s. You can finish now, or keep recording to confirm.", r.Main.Title())
	case hasCause && lv.AwakeFor >= th.LikelyMinAwake.Duration && lv.ProblemFor >= th.LikelyMinBad.Duration && share >= th.LikelyMinShare:
		lv.State = Likely
		lv.Headline = fmt.Sprintf("Likely cause so far: %s (based on %s in %s). This can still change.",
			r.Main.Title(), plural(mainIncidents, "problem"), humanDuration(lv.AwakeFor))
	case lv.AwakeFor >= th.QuietMinAwake.Duration && r.ProblemPct < th.QuietMaxProblemPct:
		lv.State = NoProblems
		lv.Headline = "No problems measured so far."
	}
	if lv.State == Likely || lv.State == Confident {
		lv.Culprit, lv.Share, lv.Incidents = r.Main, share, mainIncidents
	}
	lv.Caveats = liveCaveats(s, r)
	return lv
}

func liveCaveats(s *aggregate.Session, r *Result) []string {
	var caveats []string
	loc := s.Meta.Location()
	evening, night := 0, 0
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Asleep {
			continue
		}
		switch h := b.Start.In(loc).Hour(); {
		case h >= 18:
			evening++
		case h >= 2 && h < 6:
			night++
		}
	}
	if hour := int(time.Hour / s.Width); evening < hour || night < hour {
		caveats = append(caveats, "Evening congestion can only be detected after recording through an evening and a night.")
	}
	unmatched := 0
	for _, m := range r.Marks {
		if m.Incident < 0 {
			unmatched++
		}
	}
	if unmatched > 0 {
		caveats = append(caveats, fmt.Sprintf("%d of your marks happened while the connection measured fine.", unmatched))
	}
	var missing []string
	seen := map[string]bool{}
	for _, u := range s.Unavailable {
		if !seen[u.Collector] {
			seen[u.Collector] = true
			missing = append(missing, CollectorTitle(u.Collector))
		}
	}
	if len(missing) > 0 {
		caveats = append(caveats, "Not measured: "+strings.Join(missing, ", ")+".")
	}
	return caveats
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func humanDuration(d time.Duration) string {
	if d < time.Hour {
		return plural(int(d.Round(time.Minute)/time.Minute), "minute")
	}
	return plural(int(d.Round(time.Hour)/time.Hour), "hour")
}
