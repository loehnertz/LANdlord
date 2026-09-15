// Package admin collects data that needs admin rights. It runs inside the elevated helper process.
package admin

import (
	"context"
	"time"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

// WlanReportFile is the name of the copied Windows Wi-Fi report inside the session directory.
const WlanReportFile = "wlanreport.html"

// maxWlanReportBytes keeps very large reports out of the HTML report.
const maxWlanReportBytes = 8 << 20

type Collector struct {
	p          platform.Platform
	sessionDir string
}

func New(p platform.Platform, sessionDir string) *Collector {
	return &Collector{p: p, sessionDir: sessionDir}
}

func (*Collector) Name() string { return record.CAdmin }

func (c *Collector) Run(ctx context.Context, sink record.Sink) error {
	if err := c.supported(); err != nil {
		return err
	}
	if file, ok := c.wlanReport(ctx); ok {
		sink.Emit(record.Info(record.CAdmin, record.NWlanReport, "", time.Now(), map[string]string{"file": file}))
	}
	if route, err := c.p.DefaultRoute(); err == nil {
		if info, err := c.p.Adapter(route); err == nil && len(info.Properties) > 0 {
			sink.Emit(record.Info(record.CAdmin, record.NDriver, "", time.Now(), info.Properties))
		}
	}
	<-ctx.Done()
	return nil
}
