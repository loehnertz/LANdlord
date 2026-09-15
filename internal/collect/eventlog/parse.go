// Package eventlog summarises Windows event logs about Wi-Fi disconnects and connectivity
// changes from the week before the recording.
package eventlog

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"time"
)

const (
	IDWlanConnectFailure = 8002
	IDWlanDisconnect     = 8003
	IDNCSIChange         = 4042
)

type Event struct {
	ID   int
	Time time.Time
}

// Parse reads wevtutil's /f:xml output, which is a sequence of <Event> elements without a root.
func Parse(r io.Reader) ([]Event, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Events []struct {
			System struct {
				EventID     int `xml:"EventID"`
				TimeCreated struct {
					SystemTime string `xml:"SystemTime,attr"`
				} `xml:"TimeCreated"`
			} `xml:"System"`
		} `xml:"Event"`
	}
	wrapped := append(append([]byte("<Events>"), data...), []byte("</Events>")...)
	if err := xml.Unmarshal(wrapped, &doc); err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(doc.Events))
	for _, e := range doc.Events {
		t, err := time.Parse(time.RFC3339Nano, e.System.TimeCreated.SystemTime)
		if err != nil {
			continue
		}
		out = append(out, Event{ID: e.System.EventID, Time: t})
	}
	return out, nil
}

type day struct {
	Disconnects     int `json:"disconnects"`
	ConnectFailures int `json:"connect_failures"`
	NCSIChanges     int `json:"ncsi_changes"`
}

// Summarize counts the events of the last 7 days before now, in total and per local day.
func Summarize(events []Event, now time.Time) (map[string]float64, string) {
	cutoff := now.Add(-7 * 24 * time.Hour)
	values := map[string]float64{"disconnects_7d": 0, "connect_failures_7d": 0, "ncsi_changes_7d": 0}
	days := map[string]*day{}
	for _, e := range events {
		if e.Time.Before(cutoff) || e.Time.After(now) {
			continue
		}
		key := e.Time.In(now.Location()).Format("2006-01-02")
		d := days[key]
		if d == nil {
			d = &day{}
			days[key] = d
		}
		switch e.ID {
		case IDWlanDisconnect:
			values["disconnects_7d"]++
			d.Disconnects++
		case IDWlanConnectFailure:
			values["connect_failures_7d"]++
			d.ConnectFailures++
		case IDNCSIChange:
			values["ncsi_changes_7d"]++
			d.NCSIChanges++
		}
	}
	daily, _ := json.Marshal(days)
	return values, string(daily)
}
