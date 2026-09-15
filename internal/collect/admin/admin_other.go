//go:build !windows

package admin

import (
	"context"
	"fmt"

	"github.com/loehnertz/LANdlord/internal/collect"
)

func (*Collector) supported() error {
	return fmt.Errorf("%w: admin extras are only available on Windows", collect.ErrPermanent)
}

func (*Collector) wlanReport(context.Context) (string, bool) { return "", false }
