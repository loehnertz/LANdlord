package diagnose

import (
	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/record"
)

// Analyze runs the full diagnosis over a session snapshot.
func Analyze(s *aggregate.Session, th config.Thresholds) Result {
	c := newCtx(s, th)
	r := Result{Verdicts: make([]BucketVerdict, len(s.Buckets))}
	for i := range s.Buckets {
		b := &s.Buckets[i]
		// Buckets during LANdlord's own speed tests are not representative: the test fills the line on purpose.
		if b.Asleep || b.SelfTest {
			continue
		}
		r.AwakeBuckets++
		symptoms := c.symptoms(b)
		if len(symptoms) == 0 {
			continue
		}
		culprit, evidence := c.classify(b)
		r.Verdicts[i] = BucketVerdict{Bad: true, Culprit: culprit, Symptoms: symptoms, Evidence: evidence}
	}
	r.PeriodicSpikes = c.applyPeriodic(s, r.Verdicts)
	applyCongestion(s, r.Verdicts)
	r.Incidents = buildIncidents(s, r.Verdicts, th)
	summarize(&r)
	r.Marks = matchMarks(s.Meta.Marks, r.Incidents, th.MarkWindow.Duration)
	if ip, ok := s.LatestInfo(record.CPublic, record.NPublicIP); ok {
		r.Country = ip.Attrs["loc"]
	}
	if len(s.SpeedTests) > 0 {
		bloat := make([]float64, 0, len(s.SpeedTests))
		for _, st := range s.SpeedTests {
			bloat = append(bloat, st.BloatMs)
		}
		r.BloatGrade = BloatGrade(aggregate.Median(bloat))
	}
	r.Findings = findings(s, c, &r)
	r.Recommendations = recommend(&r)
	r.Live = liveVerdict(s, &r, th)
	return r
}
