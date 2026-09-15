//go:build !windows

package eventlog

import (
	"context"
	"fmt"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/record"
)

type Collector struct{}

func New() *Collector { return &Collector{} }

func (*Collector) Name() string { return record.CEventLog }

func (*Collector) Run(context.Context, record.Sink) error {
	return fmt.Errorf("%w: the event log history is only available on Windows", collect.ErrPermanent)
}
