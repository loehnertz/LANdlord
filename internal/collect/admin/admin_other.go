//go:build !windows

package admin

import (
	"context"
	"fmt"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/record"
)

func (*Collector) supported() error {
	return fmt.Errorf("%w: admin extras are only available on Windows", collect.ErrPermanent)
}

func (*Collector) wlanReport(context.Context) (string, bool) { return "", false }

func (*Collector) traceLoop(ctx context.Context, _ record.Sink) { <-ctx.Done() }
