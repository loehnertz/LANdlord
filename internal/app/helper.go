package app

import (
	"context"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/collect/admin"
	"github.com/loehnertz/LANdlord/internal/collect/eventlog"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/store"
)

type HelperOptions struct {
	SessionDir string
	ParentPID  int
	MaxBytes   int64
}

// RunHelper is the elevated helper process: it collects admin-only data into the session
// directory of the main process and exits together with it.
func RunHelper(ctx context.Context, plat platform.Platform, opts HelperOptions) error {
	sess, err := store.Open(opts.SessionDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if opts.ParentPID > 0 {
		go func() {
			waitForProcessExit(ctx, opts.ParentPID)
			cancel()
		}()
	}
	w := sess.NewWriter("helper", opts.MaxBytes)
	flushed := make(chan struct{})
	go func() {
		w.Run(ctx, 5*time.Second)
		close(flushed)
	}()
	sup := collect.NewSupervisor(w, nil)
	sup.Add(eventlog.New())
	sup.Add(admin.New(plat, opts.SessionDir))
	sup.Run(ctx)
	<-flushed
	return nil
}
