//go:build windows

package winplat

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Mirrors of the wlanapi.h structures. Go's field alignment matches the C layout on 64-bit Windows.

type dot11SSID struct {
	Length uint32
	SSID   [32]byte
}

type wlanInterfaceInfo struct {
	InterfaceGUID windows.GUID
	Description   [256]uint16
	State         uint32
}

type wlanInterfaceInfoListHeader struct {
	NumberOfItems uint32
	Index         uint32
}

type wlanAssociationAttributes struct {
	SSID          dot11SSID
	BSSType       uint32
	BSSID         [6]byte
	PhyType       uint32
	PhyIndex      uint32
	SignalQuality uint32
	RxRate        uint32 // kbit/s
	TxRate        uint32 // kbit/s
}

type wlanSecurityAttributes struct {
	SecurityEnabled int32
	OneXEnabled     int32
	AuthAlgorithm   uint32
	CipherAlgorithm uint32
}

type wlanConnectionAttributes struct {
	State          uint32
	ConnectionMode uint32
	ProfileName    [256]uint16
	Association    wlanAssociationAttributes
	Security       wlanSecurityAttributes
}

type wlanMacFrameStatistics struct {
	Counters [12]uint64
}

type wlanPhyFrameStatistics struct {
	TransmittedFrameCount            uint64
	MulticastTransmittedFrameCount   uint64
	FailedCount                      uint64
	RetryCount                       uint64
	MultipleRetryCount               uint64
	MaxTXLifetimeExceededCount       uint64
	TransmittedFragmentCount         uint64
	RTSSuccessCount                  uint64
	RTSFailureCount                  uint64
	ACKFailureCount                  uint64
	ReceivedFrameCount               uint64
	MulticastReceivedFrameCount      uint64
	PromiscuousReceivedFrameCount    uint64
	MaxRXLifetimeExceededCount       uint64
	FrameDuplicateCount              uint64
	ReceivedFragmentCount            uint64
	PromiscuousReceivedFragmentCount uint64
	FCSErrorCount                    uint64
}

// wlanStatisticsHeader is WLAN_STATISTICS up to its variable-length PHY array.
type wlanStatisticsHeader struct {
	FourWayHandshakeFailures   uint64
	TKIPCounterMeasuresInvoked uint64
	Reserved                   uint64
	MacUcastCounters           wlanMacFrameStatistics
	MacMcastCounters           wlanMacFrameStatistics
	NumberOfPhys               uint32
	_                          uint32
}

type wlanBSSListHeader struct {
	TotalSize     uint32
	NumberOfItems uint32
}

type wlanBSSEntry struct {
	SSID                  dot11SSID
	PhyID                 uint32
	BSSID                 [6]byte
	BSSType               uint32
	PhyType               uint32
	RSSI                  int32
	LinkQuality           uint32
	InRegDomain           uint8
	BeaconPeriod          uint16
	Timestamp             uint64
	HostTimestamp         uint64
	CapabilityInformation uint16
	ChCenterFrequency     uint32 // kHz
	RateSetLength         uint32
	RateSet               [126]uint16
	IEOffset              uint32
	IESize                uint32
}

type wlanNotificationData struct {
	Source        uint32
	Code          uint32
	InterfaceGUID windows.GUID
	DataSize      uint32
	Data          unsafe.Pointer
}

// Compile-time layout checks against the sizes in the Windows SDK.
var (
	_ [360]byte = [unsafe.Sizeof(wlanBSSEntry{})]byte{}
	_ [224]byte = [unsafe.Sizeof(wlanStatisticsHeader{})]byte{}
	_ [144]byte = [unsafe.Sizeof(wlanPhyFrameStatistics{})]byte{}
	_ [68]byte  = [unsafe.Sizeof(wlanAssociationAttributes{})]byte{}
	_ [40]byte  = [unsafe.Sizeof(wlanNotificationData{})]byte{}
)
