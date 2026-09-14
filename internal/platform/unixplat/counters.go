//go:build !windows

package unixplat

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/loehnertz/LANdlord/internal/platform"
)

// InterfaceCounters reads Linux sysfs statistics. macOS has no equivalent without cgo.
func (*Platform) InterfaceCounters(index int) (platform.Counters, error) {
	if runtime.GOOS != "linux" {
		return platform.Counters{}, platform.ErrUnsupported
	}
	ifc, err := net.InterfaceByIndex(index)
	if err != nil {
		return platform.Counters{}, err
	}
	return readSysfsCounters(filepath.Join("/sys/class/net", ifc.Name))
}

func readSysfsCounters(dir string) (platform.Counters, error) {
	read := func(name string) (uint64, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return 0, err
		}
		return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	}
	var c platform.Counters
	var err error
	if c.RxBytes, err = read("statistics/rx_bytes"); err != nil {
		return c, err
	}
	if c.TxBytes, err = read("statistics/tx_bytes"); err != nil {
		return c, err
	}
	c.InErrors, _ = read("statistics/rx_errors")
	c.OutErrors, _ = read("statistics/tx_errors")
	c.InDiscards, _ = read("statistics/rx_dropped")
	c.OutDiscards, _ = read("statistics/tx_dropped")
	if mbps, err := read("speed"); err == nil {
		c.LinkBps = mbps * 1_000_000
	}
	return c, nil
}
