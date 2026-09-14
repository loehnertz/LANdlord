package report

import (
	"encoding/csv"
	"io"
	"strconv"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/diagnose"
)

var csvHeader = []string{
	"time", "gateway_p95_ms", "gateway_loss_pct", "hop_p95_ms", "hop_loss_pct",
	"internet_p95_ms", "internet_loss_pct", "internet_jitter_ms", "udp_loss_pct", "udp_jitter_ms",
	"rssi_dbm", "rx_mbps", "router_util_pct", "dns_ms", "asleep", "problem", "culprit",
}

// WriteCSV writes one row per bucket with the charted values and the diagnosis.
func WriteCSV(w io.Writer, s *aggregate.Session, r diagnose.Result) error {
	return writeCSV(w, s, r, computeSeries(s))
}

func writeCSV(w io.Writer, s *aggregate.Session, r diagnose.Result, sr series) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	loc := s.Meta.Location()
	num := func(v *float64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatFloat(*v, 'f', 2, 64)
	}
	for i, b := range s.Buckets {
		problem, culprit := "false", ""
		if i < len(r.Verdicts) && r.Verdicts[i].Bad {
			problem, culprit = "true", string(r.Verdicts[i].Culprit)
		}
		row := []string{
			b.Start.In(loc).Format(time.RFC3339),
			num(sr.gwP95[i]), num(sr.gwLoss[i]), num(sr.hopP95[i]), num(sr.hopLoss[i]),
			num(sr.inetP95[i]), num(sr.inetLoss[i]), num(sr.inetJitter[i]), num(sr.udpLoss[i]), num(sr.udpJitter[i]),
			num(sr.rssi[i]), num(sr.rx[i]), num(sr.routerUtil[i]), num(sr.dnsMs[i]),
			strconv.FormatBool(b.Asleep), problem, culprit,
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
