//go:build windows

package eventlog

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/record"
)

type Collector struct{}

func New() *Collector { return &Collector{} }

func (*Collector) Name() string { return record.CEventLog }

func query(ctx context.Context, log, filter string) ([]Event, error) {
	q := fmt.Sprintf("*[System[(%s) and TimeCreated[timediff(@SystemTime) <= 604800000]]]", filter)
	cmd := exec.CommandContext(ctx, "wevtutil.exe", "qe", log, "/q:"+q, "/f:xml", "/c:5000")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return Parse(&out)
}

// Run reads the history once and then idles until the recording ends.
func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	qctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	wlan, err := query(qctx, "Microsoft-Windows-WLAN-AutoConfig/Operational", "EventID=8002 or EventID=8003")
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: the Windows event log couldn't be read (it may need admin rights)", collect.ErrPermanent)
	}
	ncsi, _ := query(qctx, "Microsoft-Windows-NCSI/Operational", "EventID=4042")
	now := time.Now()
	values, daily := Summarize(append(wlan, ncsi...), now)
	sink.Emit(record.Record{Time: now, Collector: record.CEventLog, Kind: record.KindInfo, Name: record.NWlanHistory,
		Values: values, Attrs: map[string]string{"daily": daily}})
	<-ctx.Done()
	return nil
}
