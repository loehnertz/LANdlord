package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/loehnertz/LANdlord/internal/aggregate"
	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/collect/admin"
	"github.com/loehnertz/LANdlord/internal/collect/dnscheck"
	"github.com/loehnertz/LANdlord/internal/collect/eventlog"
	"github.com/loehnertz/LANdlord/internal/collect/httpcheck"
	"github.com/loehnertz/LANdlord/internal/collect/mtu"
	"github.com/loehnertz/LANdlord/internal/collect/ping"
	"github.com/loehnertz/LANdlord/internal/collect/publicip"
	"github.com/loehnertz/LANdlord/internal/collect/speed"
	"github.com/loehnertz/LANdlord/internal/collect/stuncheck"
	"github.com/loehnertz/LANdlord/internal/collect/system"
	"github.com/loehnertz/LANdlord/internal/collect/traceroute"
	"github.com/loehnertz/LANdlord/internal/collect/wifi"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/diagnose"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
	"github.com/loehnertz/LANdlord/internal/report"
	"github.com/loehnertz/LANdlord/internal/router"
	"github.com/loehnertz/LANdlord/internal/router/fritzbox"
	"github.com/loehnertz/LANdlord/internal/store"
	"github.com/loehnertz/LANdlord/internal/ui"
	"github.com/loehnertz/LANdlord/internal/version"
)

const collectingHeadline = "Still collecting data. Keep using the laptop normally and press the button when something goes wrong."

type RunOptions struct {
	// NoBrowser also suppresses revealing the finished report in Explorer/Finder.
	NoHelper, NoBrowser, NoTray bool
	DataDir                     string
	// ReportDir overrides where reports go (default: Documents\LANdlord).
	ReportDir string
	// Collectors replaces the real collectors, for tests.
	Collectors func(r *Recorder) []collect.Collector
	// LingerAfterFinish keeps the status page reachable after the report is written.
	LingerAfterFinish time.Duration
	AnalyzeEvery      time.Duration
}

// Recorder runs one recording session. It implements ui.Controller and record.Sink.
type Recorder struct {
	cfg     config.Config
	plat    platform.Platform
	opts    RunOptions
	dataDir string
	sess    *store.Session
	writer  *store.Writer

	builderMu sync.Mutex
	builder   *aggregate.Builder

	resultMu sync.Mutex
	result   *diagnose.Result

	sup    *collect.Supervisor
	server *ui.Server

	locationDenied atomic.Bool
	helperRunning  atomic.Bool
	fritzBox       atomic.Bool
	fritzStarted   atomic.Bool
	finishing      atomic.Bool

	credsMu                sync.Mutex
	routerUser, routerPass string

	triggers []func()

	stopCollectors context.CancelFunc
	supDone        chan struct{}
	finishOnce     sync.Once
	finished       chan struct{}
	reportPath     string
	finishErr      error
}

var _ ui.Controller = (*Recorder)(nil)

func (r *Recorder) Emit(rec record.Record) {
	r.writer.Emit(rec)
	r.builderMu.Lock()
	r.builder.Add(rec)
	r.builderMu.Unlock()
}

// Run records until the configured duration ends, the user finishes, or ctx is cancelled
// (then the session stays unfinished and resumes on the next start).
func Run(ctx context.Context, cfg config.Config, plat platform.Platform, opts RunOptions) error {
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = cfg.DataDir
	}
	if dataDir == "" {
		d, err := plat.DataDir()
		if err != nil {
			return fmt.Errorf("find data folder: %w", err)
		}
		dataDir = d
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data folder: %w", err)
	}
	if url, ok := existingInstance(dataDir); ok {
		if !opts.NoBrowser {
			_ = plat.OpenURL(url)
		}
		return nil
	}
	if opts.LingerAfterFinish == 0 {
		opts.LingerAfterFinish = 10 * time.Minute
	}
	if opts.AnalyzeEvery == 0 {
		opts.AnalyzeEvery = 30 * time.Second
	}

	sess, resumed, err := store.OpenOrResume(dataDir, time.Now(), cfg.Duration.Duration, version.String())
	if err != nil {
		return fmt.Errorf("open session: %w", err)
	}
	r := &Recorder{
		cfg: cfg, plat: plat, opts: opts, dataDir: dataDir, sess: sess,
		writer:   sess.NewWriter("records", int64(cfg.MaxDataMB)<<20),
		finished: make(chan struct{}),
		supDone:  make(chan struct{}),
	}
	meta := sess.Meta()
	r.builder = aggregate.NewBuilder(meta, aggregate.DefaultWidth)
	if resumed {
		_ = sess.ReadRecords(func(rec record.Record) error {
			r.builder.Add(rec)
			return nil
		})
	}
	r.Emit(record.Event(record.CApp, record.NStart, time.Now(), map[string]string{"version": version.String(), "resumed": strconv.FormatBool(resumed)}))

	collectCtx, stopCollectors := context.WithCancel(ctx)
	r.stopCollectors = stopCollectors
	defer stopCollectors()
	r.sup = collect.NewSupervisor(r, nil)
	collectors := r.buildCollectors()
	if opts.Collectors != nil {
		collectors = opts.Collectors(r)
	}
	for _, c := range collectors {
		r.sup.Add(c)
	}
	go func() {
		r.sup.Run(collectCtx)
		close(r.supDone)
	}()
	go r.flushLoop(collectCtx)
	go r.analyzeLoop(collectCtx)

	serverCtx, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	if r.server, err = ui.New(r); err != nil {
		return err
	}
	if err := r.server.Start(serverCtx); err != nil {
		return fmt.Errorf("start status page: %w", err)
	}
	_ = writeRunning(dataDir, runningInfo{BaseURL: r.server.BaseURL(), Token: r.server.Token(), PID: os.Getpid()})
	defer removeRunning(dataDir)
	if cfg.UI.OpenBrowser && !opts.NoBrowser {
		_ = plat.OpenURL(r.server.URL())
	}

	quit := make(chan struct{})
	var quitOnce sync.Once
	if !opts.NoTray {
		ui.RunTray(serverCtx, ui.TrayActions{
			Open:   func() { _ = plat.OpenURL(r.server.URL()) },
			Mark:   func() { _ = r.Mark("") },
			Finish: func() { go func() { _, _ = r.Finish() }() },
			Quit:   func() { quitOnce.Do(func() { close(quit) }) },
		})
	}

	r.maybeStartHelper(meta)

	remaining := time.Until(meta.Start.Add(meta.Duration))
	timer := time.NewTimer(max(remaining, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		r.pause()
		return nil
	case <-quit:
		r.pause()
		return nil
	case <-timer.C:
		_, err = r.Finish()
	case <-r.finished:
		err = r.finishErr
	}

	select {
	case <-ctx.Done():
	case <-quit:
	case <-time.After(opts.LingerAfterFinish):
	}
	return err
}

// pause stops recording without finishing, so the next start resumes the session.
func (r *Recorder) pause() {
	r.stopCollectors()
	<-r.supDone
	r.writer.Emit(record.Event(record.CApp, record.NStop, time.Now(), map[string]string{"reason": "paused"}))
	_ = r.writer.Close()
}

func (r *Recorder) flushLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = r.writer.Flush()
		}
	}
}

func (r *Recorder) analyzeLoop(ctx context.Context) {
	first := time.NewTimer(5 * time.Second)
	defer first.Stop()
	t := time.NewTicker(r.opts.AnalyzeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			r.analyze()
		case <-t.C:
			r.analyze()
		}
	}
}

func (r *Recorder) snapshot() *aggregate.Session {
	meta := r.sess.Meta()
	r.builderMu.Lock()
	defer r.builderMu.Unlock()
	return r.builder.Snapshot(meta)
}

func (r *Recorder) analyze() diagnose.Result {
	res := diagnose.Analyze(r.snapshot(), r.cfg.Thresholds)
	r.resultMu.Lock()
	r.result = &res
	r.resultMu.Unlock()
	return res
}

func (r *Recorder) maybeStartHelper(meta store.Meta) {
	if runtime.GOOS != "windows" || r.opts.NoHelper || !r.cfg.Helper.Enabled || r.plat.IsElevated() {
		return
	}
	notMeasured := func(reason string) {
		for _, c := range []string{record.CEventLog, record.CAdmin} {
			r.Emit(record.Unavailable(c, time.Now(), reason))
		}
	}
	if meta.HelperAsked {
		return
	}
	_ = r.sess.Update(func(m *store.Meta) { m.HelperAsked = true })
	go func() {
		args := []string{"--helper", "--session", r.sess.Dir(), "--parent", strconv.Itoa(os.Getpid())}
		switch err := r.plat.RelaunchElevated(args); {
		case err == nil:
			r.helperRunning.Store(true)
		case errors.Is(err, platform.ErrDeclined):
			notMeasured("the admin prompt was declined")
		default:
			notMeasured("the admin helper couldn't start: " + err.Error())
		}
	}()
}

func parseAddrs(list []string) []netip.Addr {
	var out []netip.Addr
	for _, s := range list {
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func (r *Recorder) buildCollectors() []collect.Collector {
	cfg := r.cfg
	targets4, targets6 := parseAddrs(cfg.Targets.IPv4), parseAddrs(cfg.Targets.IPv6)
	hops := &ping.HopSet{}
	sys := system.New(r.plat, 2*time.Second)
	pub := publicip.New("", 30*time.Minute, nil)
	trace := traceroute.New(r.plat, targets4[:min(2, len(targets4))], hops, 5*time.Minute)
	wf := wifi.New(r.plat, 2*time.Second, 10*time.Minute, func(denied bool) { r.locationDenied.Store(denied) })

	list := []collect.Collector{
		ping.New(r.plat, ping.Config{Targets4: targets4, Targets6: targets6, Interval: time.Second}, hops),
		trace, wf, sys,
		dnscheck.New(r.plat, cfg.DNS.Hosts, 30*time.Second),
		httpcheck.New(cfg.HTTP.Targets, time.Minute),
		stuncheck.New(cfg.STUN.Servers, time.Minute, 50, 10*time.Second),
		pub,
		router.NewIdentityCollector(r.plat, r.onRouterIdentity),
		router.NewUPnPCollector(30*time.Second, pub.IP, r.ipv6Works),
	}
	if len(targets4) > 0 {
		list = append(list, mtu.New(r.plat, targets4[0], time.Hour))
	}
	r.triggers = []func(){trace.Trigger, wf.Trigger}
	if cfg.Speedtest.Enabled {
		sc := speed.DefaultConfig()
		sc.Interval = cfg.Speedtest.Interval.Duration
		sc.MaxDownloadBytes = int64(cfg.Speedtest.MaxDownloadMB) << 20
		sc.MaxUploadBytes = int64(cfg.Speedtest.MaxUploadMB) << 20
		if len(targets4) > 0 {
			sc.BloatTarget = targets4[0]
		}
		sp := speed.New(r.plat, sc, sys.LaptopMbps)
		list = append(list, sp)
		r.triggers = append(r.triggers, sp.Trigger)
	}
	if runtime.GOOS == "windows" && r.plat.IsElevated() {
		list = append(list, eventlog.New(), admin.New(r.plat, r.sess.Dir()))
	}
	return list
}

func (r *Recorder) ipv6Works() bool {
	route, err := r.plat.DefaultRoute()
	return err == nil && route.Gateway6.IsValid()
}

func (r *Recorder) onRouterIdentity(id router.Identity, gateway netip.Addr) {
	r.fritzBox.Store(id.FritzBox)
	if id.FritzBox && r.fritzStarted.CompareAndSwap(false, true) {
		r.sup.Launch(fritzbox.New(gateway, r.routerCredentials, time.Minute))
	}
}

func (r *Recorder) routerCredentials() (string, string, bool) {
	r.credsMu.Lock()
	defer r.credsMu.Unlock()
	return r.routerUser, r.routerPass, r.routerPass != ""
}

// ui.Controller

func (r *Recorder) Status() ui.Status {
	meta := r.sess.Meta()
	end := meta.Start.Add(meta.Duration)
	st := ui.Status{
		Recording:        !meta.Finished,
		Finishing:        r.finishing.Load(),
		StartedAt:        meta.Start,
		EndsAt:           end,
		RemainingSeconds: max(0, int64(time.Until(end).Seconds())),
		Marks:            len(meta.Marks),
		LocationDenied:   r.locationDenied.Load(),
		HelperRunning:    r.helperRunning.Load(),
		RouterIsFritzBox: r.fritzBox.Load(),
		ContractMbps:     meta.ContractMbps,
		ReportPath:       meta.ReportPath,
		Live:             ui.LiveView{State: string(diagnose.Collecting), Headline: collectingHeadline},
	}
	_, _, hasCreds := r.routerCredentials()
	st.RouterNeedsPassword = st.RouterIsFritzBox && !hasCreds
	if meta.Finished && r.finishErr != nil {
		st.Error = "Creating the report failed: " + r.finishErr.Error()
	}
	r.resultMu.Lock()
	res := r.result
	r.resultMu.Unlock()
	if res != nil {
		lv := res.Live
		st.Live = ui.LiveView{
			State:          string(lv.State),
			Headline:       lv.Headline,
			SharePct:       lv.Share * 100,
			Caveats:        lv.Caveats,
			AwakeMinutes:   lv.AwakeFor.Minutes(),
			ProblemMinutes: lv.ProblemFor.Minutes(),
		}
		if lv.Culprit != "" {
			st.Live.Culprit = lv.Culprit.Title()
		}
	}
	for _, s := range r.sup.States() {
		st.Collectors = append(st.Collectors, ui.CollectorView{Name: s.Name, Status: s.Status, LastError: s.LastError})
	}
	return st
}

func (r *Recorder) Mark(tag string) error {
	if r.sess.Meta().Finished || r.finishing.Load() {
		return errors.New("the recording has already finished")
	}
	if err := r.sess.AddMark(store.Mark{Time: time.Now(), Tag: tag}); err != nil {
		return err
	}
	for _, trigger := range r.triggers {
		trigger()
	}
	go r.analyze()
	return nil
}

func (r *Recorder) Finish() (string, error) {
	r.finishOnce.Do(func() {
		r.finishing.Store(true)
		defer r.finishing.Store(false)
		defer close(r.finished)
		r.stopCollectors()
		<-r.supDone
		r.writer.Emit(record.Event(record.CApp, record.NStop, time.Now(), map[string]string{"reason": "finished"}))
		if err := r.writer.Close(); err != nil {
			r.finishErr = fmt.Errorf("save measurements: %w", err)
		}
		finishedAt := time.Now()
		if err := r.sess.Update(func(m *store.Meta) { m.Finished, m.FinishedAt = true, finishedAt }); err != nil {
			r.finishErr = err
			return
		}
		meta := r.sess.Meta()
		reportDir := r.opts.ReportDir
		if reportDir == "" {
			docs, err := r.plat.DocumentsDir()
			if err != nil || docs == "" {
				docs = r.dataDir
			}
			reportDir = filepath.Join(docs, "LANdlord")
		}
		name := meta.Start.In(meta.Location()).Format("2006-01-02_1504")
		out := filepath.Join(reportDir, name, "report.html")
		if err := RebuildReport(r.sess.Dir(), out, false, r.cfg.Thresholds); err != nil {
			r.finishErr = err
			return
		}
		_ = r.sess.Update(func(m *store.Meta) { m.ReportPath = out })
		r.reportPath = out
		if !r.opts.NoBrowser {
			_ = r.plat.RevealFile(out)
		}
	})
	return r.reportPath, r.finishErr
}

func (r *Recorder) SetRouterCredentials(user, pass string) {
	r.credsMu.Lock()
	defer r.credsMu.Unlock()
	r.routerUser, r.routerPass = user, pass
}

func (r *Recorder) SetContractMbps(mbps float64) error {
	if mbps < 0 || mbps > 100_000 {
		return errors.New("please enter a speed between 1 and 100000 Mbit/s")
	}
	return r.sess.Update(func(m *store.Meta) { m.ContractMbps = mbps })
}

func (r *Recorder) ReportSoFar(w io.Writer) error {
	snap := r.snapshot()
	res := diagnose.Analyze(snap, r.cfg.Thresholds)
	return report.Render(w, snap, res, report.Options{
		Partial:    !snap.Meta.Finished,
		Thresholds: &r.cfg.Thresholds,
		WlanReport: readWlanReport(r.sess.Dir()),
	})
}

func (r *Recorder) OpenLocationSettings() error {
	return r.plat.OpenURL("ms-settings:privacy-location")
}
