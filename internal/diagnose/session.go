package diagnose

import (
	"fmt"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
)

// applyPeriodic detects router ping spikes that recur about once a minute (Windows background
// Wi-Fi scans) and attributes those buckets to the laptop.
func (c *sessionCtx) applyPeriodic(s *aggregate.Session, v []BucketVerdict) bool {
	limit := max(20, 3*c.gwMedian)
	var spikes []int
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Asleep {
			continue
		}
		if p := gatewayPath(b); p != nil && p.P95 > limit {
			spikes = append(spikes, i)
		}
	}
	if len(spikes) < 10 {
		return false
	}
	regular := 0
	for k := 1; k < len(spikes); k++ {
		sec := float64(spikes[k]-spikes[k-1]) * s.Width.Seconds()
		if sec >= 50 && sec <= 70 {
			regular++
		}
	}
	if float64(regular) < 0.6*float64(len(spikes)-1) {
		return false
	}
	ev := Evidence{"periodic", "Router ping spikes repeat about every 60 seconds, which matches Windows background Wi-Fi scanning"}
	for _, i := range spikes {
		if v[i].Bad && (v[i].Culprit == ClientDevice || v[i].Culprit == Unknown) {
			v[i].Culprit = ClientDevice
			v[i].Evidence = append(v[i].Evidence, ev)
		}
	}
	return true
}

// applyCongestion relabels evening access-line problems as provider congestion when nights are clean.
func applyCongestion(s *aggregate.Session, v []BucketVerdict) {
	loc := s.Meta.Location()
	var access, evening []int
	nightAwake, nightBad := 0, 0
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.Asleep {
			continue
		}
		h := b.Start.In(loc).Hour()
		if h >= 2 && h < 6 {
			nightAwake++
			if v[i].Bad {
				nightBad++
			}
		}
		if v[i].Bad && v[i].Culprit == AccessLine {
			access = append(access, i)
			if h >= 18 {
				evening = append(evening, i)
			}
		}
	}
	hour := int(time.Hour / s.Width)
	if len(access) < 6 || nightAwake < hour || float64(nightBad) >= 0.01*float64(nightAwake) ||
		float64(len(evening)) < 0.6*float64(len(access)) {
		return
	}
	ev := Evidence{"evening", fmt.Sprintf("%.0f%% of these problems happened between 18:00 and 24:00, while nights were clean", float64(len(evening))/float64(len(access))*100)}
	for _, i := range evening {
		v[i].Culprit = ISPCongestion
		v[i].Evidence = append(v[i].Evidence, ev)
	}
}
