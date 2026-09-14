package platform

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Fake is a scriptable Platform for tests.
type Fake struct {
	mu sync.Mutex

	EchoFunc     func(EchoRequest) (EchoReply, error)
	RouteValue   Route
	RouteErr     error
	MAC          net.HardwareAddr
	CountersFunc func(index int) (Counters, error)
	AdapterValue AdapterInfo
	PowerValue   PowerStatus
	PowerErr     error
	Wifi         *FakeWifi
	WifiErr      error
	Elevated     bool
	RelaunchErr  error
	Docs, Data   string

	Relaunched [][]string
	OpenedURLs []string
	Revealed   []string
	Echoes     []EchoRequest
}

var _ Platform = (*Fake)(nil)

func (f *Fake) NewPinger(int) (Pinger, error) { return &fakePinger{f: f}, nil }

func (f *Fake) SetRoute(r Route) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RouteValue = r
}

func (f *Fake) DefaultRoute() (Route, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.RouteValue, f.RouteErr
}

func (f *Fake) NeighborMAC(netip.Addr) (net.HardwareAddr, error) {
	if f.MAC == nil {
		return nil, ErrUnsupported
	}
	return f.MAC, nil
}

func (f *Fake) InterfaceCounters(index int) (Counters, error) {
	if f.CountersFunc == nil {
		return Counters{}, ErrUnsupported
	}
	return f.CountersFunc(index)
}

func (f *Fake) Adapter(Route) (AdapterInfo, error) { return f.AdapterValue, nil }

func (f *Fake) Power() (PowerStatus, error) { return f.PowerValue, f.PowerErr }

func (f *Fake) KeepAwake(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (f *Fake) OpenWifi() (Wifi, error) {
	if f.WifiErr != nil {
		return nil, f.WifiErr
	}
	if f.Wifi == nil {
		return nil, ErrUnsupported
	}
	return f.Wifi, nil
}

func (f *Fake) IsElevated() bool { return f.Elevated }

func (f *Fake) RelaunchElevated(args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Relaunched = append(f.Relaunched, args)
	return f.RelaunchErr
}

func (f *Fake) OpenURL(url string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.OpenedURLs = append(f.OpenedURLs, url)
	return nil
}

func (f *Fake) RevealFile(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Revealed = append(f.Revealed, path)
	return nil
}

func (f *Fake) DocumentsDir() (string, error) { return f.Docs, nil }
func (f *Fake) DataDir() (string, error)      { return f.Data, nil }

type fakePinger struct{ f *Fake }

func (p *fakePinger) Echo(ctx context.Context, req EchoRequest) (EchoReply, error) {
	p.f.mu.Lock()
	p.f.Echoes = append(p.f.Echoes, req)
	fn := p.f.EchoFunc
	p.f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return EchoReply{}, err
	}
	if fn == nil {
		return EchoReply{From: req.Dst, RTT: time.Millisecond, Status: EchoOK}, nil
	}
	return fn(req)
}

func (p *fakePinger) Close() error { return nil }

// FakeWifi is a scriptable Wifi for tests.
type FakeWifi struct {
	mu            sync.Mutex
	LinkValue     WifiLink
	LinkErr       error
	CountersValue WifiCounters
	BSSValue      []BSS
	BSSErr        error
	Scans         int
	EventsCh      chan WifiEvent
}

var _ Wifi = (*FakeWifi)(nil)

func (w *FakeWifi) Set(fn func(w *FakeWifi)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fn(w)
}

func (w *FakeWifi) Link() (WifiLink, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.LinkValue, w.LinkErr
}

func (w *FakeWifi) Counters() (WifiCounters, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.CountersValue, nil
}

func (w *FakeWifi) Scan() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Scans++
	return w.BSSErr
}

func (w *FakeWifi) BSSList() ([]BSS, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.BSSValue, w.BSSErr
}

func (w *FakeWifi) Events(context.Context) (<-chan WifiEvent, error) {
	if w.EventsCh == nil {
		return nil, ErrUnsupported
	}
	return w.EventsCh, nil
}

func (w *FakeWifi) Close() error { return nil }
