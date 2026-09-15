//go:build windows

package winplat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

var (
	wlanapi                      = windows.NewLazySystemDLL("wlanapi.dll")
	procWlanOpenHandle           = wlanapi.NewProc("WlanOpenHandle")
	procWlanCloseHandle          = wlanapi.NewProc("WlanCloseHandle")
	procWlanEnumInterfaces       = wlanapi.NewProc("WlanEnumInterfaces")
	procWlanQueryInterface       = wlanapi.NewProc("WlanQueryInterface")
	procWlanScan                 = wlanapi.NewProc("WlanScan")
	procWlanGetNetworkBssList    = wlanapi.NewProc("WlanGetNetworkBssList")
	procWlanRegisterNotification = wlanapi.NewProc("WlanRegisterNotification")
	procWlanFreeMemory           = wlanapi.NewProc("WlanFreeMemory")
)

const (
	opcodeCurrentConnection = 7
	opcodeStatistics        = 0x10000101
	opcodeRSSI              = 0x10000102

	interfaceStateConnected = 1
	dot11BSSTypeAny         = 3

	notificationSourceACM = 0x08
	notificationSourceMSM = 0x10
	acmConnectionComplete = 10
	acmDisconnected       = 21
	msmRoamingEnd         = 6

	// Offset of wlanReasonCode in WLAN_CONNECTION_NOTIFICATION_DATA.
	connectionNotificationReasonOffset = 560

	errAccessDenied = 5
	errInvalidState = 5023
)

var phyNames = map[uint32]string{4: "a", 5: "b", 6: "g", 7: "n", 8: "ac", 10: "ax", 11: "be"}

type wifi struct {
	handle windows.Handle
	guid   windows.GUID

	mu       sync.Mutex
	freq     map[string]int
	freqAt   time.Time
	register sync.Once
}

// Windows limits how many callbacks a process can create, so a single one dispatches to the
// channel of whichever Wi-Fi handle is currently listening.
var (
	notificationCallback     uintptr
	notificationCallbackOnce sync.Once
	notificationTarget       atomic.Pointer[chan platform.WifiEvent]
)

func (*Platform) OpenWifi() (platform.Wifi, error) {
	if err := procWlanOpenHandle.Find(); err != nil {
		return nil, fmt.Errorf("%w: %w", platform.ErrUnsupported, err)
	}
	var negotiated uint32
	var h windows.Handle
	if r, _, _ := procWlanOpenHandle.Call(2, 0, uintptr(unsafe.Pointer(&negotiated)), uintptr(unsafe.Pointer(&h))); r != 0 {
		// Typically the WLAN AutoConfig service isn't running because the computer has no Wi-Fi.
		return nil, fmt.Errorf("%w: Wi-Fi service unavailable (%v)", platform.ErrUnsupported, windows.Errno(r))
	}
	var list *wlanInterfaceInfoListHeader
	if r, _, _ := procWlanEnumInterfaces.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&list))); r != 0 {
		procWlanCloseHandle.Call(uintptr(h), 0)
		return nil, windows.Errno(r)
	}
	defer procWlanFreeMemory.Call(uintptr(unsafe.Pointer(list)))
	if list.NumberOfItems == 0 {
		procWlanCloseHandle.Call(uintptr(h), 0)
		return nil, fmt.Errorf("%w: no Wi-Fi adapter", platform.ErrUnsupported)
	}
	entries := unsafe.Slice((*wlanInterfaceInfo)(unsafe.Add(unsafe.Pointer(list), unsafe.Sizeof(*list))), list.NumberOfItems)
	chosen := entries[0]
	for _, e := range entries {
		if e.State == interfaceStateConnected {
			chosen = e
			break
		}
	}
	return &wifi{handle: h, guid: chosen.InterfaceGUID}, nil
}

func (w *wifi) Close() error {
	notificationTarget.Store(nil)
	if r, _, _ := procWlanCloseHandle.Call(uintptr(w.handle), 0); r != 0 {
		return windows.Errno(r)
	}
	return nil
}

func (w *wifi) query(opcode uint32) (unsafe.Pointer, uint32, error) {
	var size uint32
	var data unsafe.Pointer
	r, _, _ := procWlanQueryInterface.Call(uintptr(w.handle), uintptr(unsafe.Pointer(&w.guid)), uintptr(opcode), 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)), 0)
	switch r {
	case 0:
		return data, size, nil
	case errAccessDenied:
		// Windows 11 24H2 and later deny Wi-Fi details when location access for desktop apps is off.
		return nil, 0, platform.ErrLocationDenied
	default:
		return nil, 0, windows.Errno(r)
	}
}

func free(p unsafe.Pointer) { procWlanFreeMemory.Call(uintptr(p)) }

func (w *wifi) Link() (platform.WifiLink, error) {
	data, _, err := w.query(opcodeCurrentConnection)
	if err != nil {
		if errors.Is(err, windows.Errno(errInvalidState)) {
			return platform.WifiLink{}, nil // not connected
		}
		return platform.WifiLink{}, err
	}
	attrs := *(*wlanConnectionAttributes)(data)
	free(data)
	if attrs.State != interfaceStateConnected {
		return platform.WifiLink{}, nil
	}
	a := attrs.Association
	link := platform.WifiLink{
		Connected:  true,
		SSID:       platform.SSIDFromBytes(a.SSID.SSID[:], int(a.SSID.Length)),
		BSSID:      net.HardwareAddr(append([]byte(nil), a.BSSID[:]...)),
		PHY:        phyNames[a.PhyType],
		QualityPct: int(a.SignalQuality),
		RxKbps:     a.RxRate,
		TxKbps:     a.TxRate,
	}
	if rssi, size, err := w.query(opcodeRSSI); err == nil {
		if size >= 4 {
			link.RSSI = int(*(*int32)(rssi))
		}
		free(rssi)
	}
	link.FreqMHz = w.frequency(link.BSSID)
	return link, nil
}

// frequency looks up the connected access point's frequency from a cached BSS list.
func (w *wifi) frequency(bssid net.HardwareAddr) int {
	w.mu.Lock()
	stale := w.freq == nil || time.Since(w.freqAt) > time.Minute
	w.mu.Unlock()
	if stale {
		if list, err := w.BSSList(); err == nil {
			freq := make(map[string]int, len(list))
			for _, b := range list {
				freq[strings.ToLower(b.BSSID.String())] = b.FreqMHz
			}
			w.mu.Lock()
			w.freq, w.freqAt = freq, time.Now()
			w.mu.Unlock()
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.freq[strings.ToLower(bssid.String())]
}

func (w *wifi) Counters() (platform.WifiCounters, error) {
	data, size, err := w.query(opcodeStatistics)
	if err != nil {
		return platform.WifiCounters{}, err
	}
	defer free(data)
	h := (*wlanStatisticsHeader)(data)
	headerSize := uint32(unsafe.Sizeof(*h))
	phySize := uint32(unsafe.Sizeof(wlanPhyFrameStatistics{}))
	n := h.NumberOfPhys
	if size < headerSize {
		return platform.WifiCounters{}, errors.New("WLAN statistics buffer too small")
	}
	n = min(n, (size-headerSize)/phySize)
	var out platform.WifiCounters
	for _, p := range unsafe.Slice((*wlanPhyFrameStatistics)(unsafe.Add(data, headerSize)), n) {
		out.TxFrames += p.TransmittedFrameCount
		out.RxFrames += p.ReceivedFrameCount
		out.Retries += p.RetryCount
		out.Failures += p.FailedCount
		out.FCSErrors += p.FCSErrorCount
	}
	return out, nil
}

func (w *wifi) Scan() error {
	if r, _, _ := procWlanScan.Call(uintptr(w.handle), uintptr(unsafe.Pointer(&w.guid)), 0, 0, 0); r != 0 {
		if r == errAccessDenied {
			return platform.ErrLocationDenied
		}
		return windows.Errno(r)
	}
	return nil
}

func (w *wifi) BSSList() ([]platform.BSS, error) {
	var list *wlanBSSListHeader
	r, _, _ := procWlanGetNetworkBssList.Call(uintptr(w.handle), uintptr(unsafe.Pointer(&w.guid)), 0, dot11BSSTypeAny, 0, 0, uintptr(unsafe.Pointer(&list)))
	switch r {
	case 0:
	case errAccessDenied:
		return nil, platform.ErrLocationDenied
	default:
		return nil, windows.Errno(r)
	}
	defer free(unsafe.Pointer(list))
	base := unsafe.Add(unsafe.Pointer(list), unsafe.Sizeof(*list))
	entrySize := unsafe.Sizeof(wlanBSSEntry{})
	out := make([]platform.BSS, 0, list.NumberOfItems)
	for i := range uintptr(list.NumberOfItems) {
		ptr := unsafe.Add(base, i*entrySize)
		e := (*wlanBSSEntry)(ptr)
		var ies []byte
		if e.IESize > 0 {
			ies = append([]byte(nil), unsafe.Slice((*byte)(unsafe.Add(ptr, e.IEOffset)), e.IESize)...)
		}
		out = append(out, platform.BSS{
			SSID:     platform.SSIDFromBytes(e.SSID.SSID[:], int(e.SSID.Length)),
			BSSID:    net.HardwareAddr(append([]byte(nil), e.BSSID[:]...)),
			RSSI:     int(e.RSSI),
			FreqMHz:  int(e.ChCenterFrequency / 1000),
			WidthMHz: platform.WidthFromIEs(ies),
		})
	}
	return out, nil
}

func (w *wifi) Events(ctx context.Context) (<-chan platform.WifiEvent, error) {
	notificationCallbackOnce.Do(func() {
		notificationCallback = windows.NewCallback(func(data *wlanNotificationData, _ uintptr) uintptr {
			target := notificationTarget.Load()
			if target == nil || data == nil {
				return 0
			}
			ev := platform.WifiEvent{Time: time.Now()}
			switch {
			case data.Source == notificationSourceACM && data.Code == acmConnectionComplete:
				ev.Name = record.NConnect
			case data.Source == notificationSourceACM && data.Code == acmDisconnected:
				ev.Name = record.NDisconnect
				if data.Data != nil && data.DataSize >= connectionNotificationReasonOffset+4 {
					ev.Reason = *(*uint32)(unsafe.Add(data.Data, connectionNotificationReasonOffset))
				}
			case data.Source == notificationSourceMSM && data.Code == msmRoamingEnd:
				ev.Name = record.NRoam
			default:
				return 0
			}
			select {
			case *target <- ev:
			default:
			}
			return 0
		})
	})
	ch := make(chan platform.WifiEvent, 32)
	notificationTarget.Store(&ch)
	r, _, _ := procWlanRegisterNotification.Call(uintptr(w.handle), notificationSourceACM|notificationSourceMSM, 0, notificationCallback, 0, 0, 0)
	if r != 0 {
		notificationTarget.Store(nil)
		return nil, windows.Errno(r)
	}
	go func() {
		<-ctx.Done()
		procWlanRegisterNotification.Call(uintptr(w.handle), 0, 0, 0, 0, 0, 0)
		notificationTarget.CompareAndSwap(&ch, nil)
	}()
	return ch, nil
}
