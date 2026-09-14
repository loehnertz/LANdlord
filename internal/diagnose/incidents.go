package diagnose

import (
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/store"
)

// buildIncidents merges bad buckets that are at most MergeGap apart.
func buildIncidents(s *aggregate.Session, v []BucketVerdict, th config.Thresholds) []Incident {
	gap := int(th.MergeGap.Duration / s.Width)
	var spans [][2]int
	for i := range v {
		if !v[i].Bad {
			continue
		}
		if n := len(spans); n > 0 && i-spans[n-1][1]-1 <= gap {
			spans[n-1][1] = i
			continue
		}
		spans = append(spans, [2]int{i, i})
	}
	out := make([]Incident, 0, len(spans))
	for _, sp := range spans {
		out = append(out, makeIncident(s, v, sp[0], sp[1]))
	}
	return out
}

func makeIncident(s *aggregate.Session, v []BucketVerdict, first, last int) Incident {
	counts := map[Culprit]int{}
	total := 0
	for i := first; i <= last; i++ {
		if v[i].Bad {
			counts[v[i].Culprit]++
			total++
		}
	}
	main := pickMain(counts)
	inc := Incident{
		Start:   s.Buckets[first].Start,
		End:     s.Buckets[last].Start.Add(s.Width),
		First:   first,
		Last:    last,
		Culprit: main,
		Shares:  map[Culprit]float64{},
	}
	for _, c := range AllCulprits {
		n := counts[c]
		if n == 0 {
			continue
		}
		share := float64(n) / float64(total)
		inc.Shares[c] = share
		if c != main && share > 0.25 {
			inc.RunnersUp = append(inc.RunnersUp, c)
		}
	}
	seenEvidence, seenSymptoms := map[string]bool{}, map[string]bool{}
	for i := first; i <= last; i++ {
		if !v[i].Bad {
			continue
		}
		for _, e := range v[i].Symptoms {
			if !seenSymptoms[e.Signal] {
				seenSymptoms[e.Signal] = true
				inc.Symptoms = append(inc.Symptoms, e)
			}
		}
		if v[i].Culprit != main {
			continue
		}
		for _, e := range v[i].Evidence {
			if !seenEvidence[e.Signal] {
				seenEvidence[e.Signal] = true
				inc.Evidence = append(inc.Evidence, e)
			}
		}
	}
	inc.Confidence = confidenceFor(strongCount(inc.Evidence))
	return inc
}

func strongCount(ev []Evidence) int {
	n := 0
	for _, e := range ev {
		if !weakSignals[e.Signal] {
			n++
		}
	}
	return n
}

// pickMain returns the culprit with the most buckets. Ties go to the earlier entry in
// AllCulprits, and Unknown only wins when nothing else was found.
func pickMain(counts map[Culprit]int) Culprit {
	best, bestN := Unknown, 0
	for _, c := range AllCulprits {
		if c != Unknown && counts[c] > bestN {
			best, bestN = c, counts[c]
		}
	}
	return best
}

func matchMarks(marks []store.Mark, incidents []Incident, window time.Duration) []MarkResult {
	out := make([]MarkResult, 0, len(marks))
	for _, m := range marks {
		mr := MarkResult{Time: m.Time, Tag: m.Tag, Incident: -1}
		for i, inc := range incidents {
			if !inc.Start.After(m.Time.Add(window)) && !inc.End.Before(m.Time.Add(-window)) {
				mr.Incident = i
				break
			}
		}
		out = append(out, mr)
	}
	return out
}

// summarize fills the session-wide counts, shares and main culprit from verdicts and incidents.
func summarize(r *Result) {
	counts := map[Culprit]int{}
	r.BadBuckets = 0
	for _, v := range r.Verdicts {
		if v.Bad {
			r.BadBuckets++
			counts[v.Culprit]++
		}
	}
	if r.AwakeBuckets > 0 {
		r.ProblemPct = float64(r.BadBuckets) / float64(r.AwakeBuckets) * 100
	}
	r.Shares = map[Culprit]float64{}
	if r.BadBuckets == 0 {
		return
	}
	for c, n := range counts {
		r.Shares[c] = float64(n) / float64(r.BadBuckets)
	}
	r.Main = pickMain(counts)
	weights := map[Confidence]int{}
	for _, inc := range r.Incidents {
		if inc.Culprit == r.Main {
			weights[inc.Confidence] += inc.Last - inc.First + 1
		}
	}
	r.MainConfidence = Low
	bestW := 0
	for _, c := range []Confidence{High, Medium, Low} {
		if weights[c] > bestW {
			r.MainConfidence, bestW = c, weights[c]
		}
	}
}
