//go:build windows

package winplat

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/loehnertz/LANdlord/internal/platform"
)

var (
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemPowerStatus    = kernel32.NewProc("GetSystemPowerStatus")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001

	networkAdapterClass = `SYSTEM\CurrentControlSet\Control\Class\{4d36e972-e325-11ce-bfc1-08002be10318}`
)

func (*Platform) InterfaceCounters(index int) (platform.Counters, error) {
	row := windows.MibIfRow2{InterfaceIndex: uint32(index)}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row); err != nil {
		return platform.Counters{}, err
	}
	return platform.Counters{
		RxBytes: row.InOctets, TxBytes: row.OutOctets,
		InErrors: row.InErrors, OutErrors: row.OutErrors,
		InDiscards: row.InDiscards, OutDiscards: row.OutDiscards,
		LinkBps: row.ReceiveLinkSpeed,
	}, nil
}

type systemPowerStatus struct {
	ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag uint8
	BatteryLifeTime, BatteryFullLifeTime                            uint32
}

func (*Platform) Power() (platform.PowerStatus, error) {
	var s systemPowerStatus
	if r, _, err := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 {
		return platform.PowerStatus{}, err
	}
	switch s.ACLineStatus {
	case 0:
		return platform.PowerStatus{Known: true, OnAC: false}, nil
	case 1:
		return platform.PowerStatus{Known: true, OnAC: true}, nil
	}
	return platform.PowerStatus{}, nil
}

// KeepAwake holds ES_SYSTEM_REQUIRED on a dedicated OS thread; the execution state belongs to
// the thread that set it, so the goroutine must not migrate.
func (*Platform) KeepAwake(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if r, _, err := procSetThreadExecutionState.Call(esContinuous | esSystemRequired); r == 0 {
			errc <- err
			return
		}
		<-ctx.Done()
		procSetThreadExecutionState.Call(esContinuous)
		errc <- nil
	}()
	return <-errc
}

// Adapter reads driver details from the network adapter class key matching the adapter GUID.
func (*Platform) Adapter(route platform.Route) (platform.AdapterInfo, error) {
	class, err := registry.OpenKey(registry.LOCAL_MACHINE, networkAdapterClass, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return platform.AdapterInfo{}, err
	}
	defer class.Close()
	names, err := class.ReadSubKeyNames(-1)
	if err != nil {
		return platform.AdapterInfo{}, err
	}
	for _, name := range names {
		key, err := registry.OpenKey(class, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		id, _, err := key.GetStringValue("NetCfgInstanceId")
		if err != nil || !strings.EqualFold(id, route.InterfaceGUID) {
			key.Close()
			continue
		}
		info := platform.AdapterInfo{Properties: map[string]string{}}
		info.DriverVersion, _, _ = key.GetStringValue("DriverVersion")
		info.DriverDate, _, _ = key.GetStringValue("DriverDate")
		if values, err := key.ReadValueNames(-1); err == nil {
			for _, v := range values {
				if len(info.Properties) >= 80 {
					break
				}
				if s, _, err := key.GetStringValue(v); err == nil && len(s) <= 100 {
					info.Properties[v] = s
				} else if n, _, err := key.GetIntegerValue(v); err == nil {
					info.Properties[v] = strconv.FormatUint(n, 10)
				}
			}
		}
		key.Close()
		return info, nil
	}
	return platform.AdapterInfo{}, fmt.Errorf("adapter %s not found in the registry", route.InterfaceGUID)
}
