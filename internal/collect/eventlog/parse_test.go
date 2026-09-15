package eventlog

import (
	"strings"
	"testing"
	"time"
)

const sample = `<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='Microsoft-Windows-WLAN-AutoConfig'/><EventID>8003</EventID><TimeCreated SystemTime='2026-09-13T21:15:02.1234567Z'/></System><EventData><Data Name='SSID'>home</Data></EventData></Event>
<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><EventID>8002</EventID><TimeCreated SystemTime='2026-09-13T21:15:30.0000000Z'/></System></Event>
<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><EventID>8003</EventID><TimeCreated SystemTime='2026-09-01T10:00:00.0000000Z'/></System></Event>
<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><EventID>4042</EventID><TimeCreated SystemTime='2026-09-14T06:00:00.0000000Z'/></System></Event>`

func TestParseAndSummarize(t *testing.T) {
	events, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].ID != IDWlanDisconnect || events[0].Time.Second() != 2 {
		t.Fatalf("events = %+v", events)
	}
	values, daily := Summarize(events, time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	if values["disconnects_7d"] != 1 || values["connect_failures_7d"] != 1 || values["ncsi_changes_7d"] != 1 {
		t.Fatalf("values = %v", values)
	}
	if !strings.Contains(daily, `"2026-09-13":{"disconnects":1,"connect_failures":1,"ncsi_changes":0}`) {
		t.Fatalf("daily = %s", daily)
	}
	empty, err := Parse(strings.NewReader(""))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty parse = %v, %v", empty, err)
	}
}
