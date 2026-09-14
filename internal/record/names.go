package record

// Collector names.
const (
	CPing     = "ping"
	CTrace    = "trace"
	CWifi     = "wifi"
	CSystem   = "system"
	CDNS      = "dns"
	CHTTP     = "http"
	CSTUN     = "stun"
	CSpeed    = "speed"
	CPublic   = "public"
	CMTU      = "mtu"
	CRouter   = "router"
	CUPnP     = "upnp"
	CFritz    = "fritz"
	CEventLog = "eventlog"
	CAdmin    = "admin"
	CApp      = "landlord"
)

// Record names.
const (
	NEcho         = "echo"
	NRoute        = "route"
	NRouteChange  = "route_change"
	NLink         = "link"
	NScan         = "scan"
	NConnect      = "connect"
	NDisconnect   = "disconnect"
	NRoam         = "roam"
	NIface        = "iface"
	NPower        = "power"
	NSleep        = "sleep"
	NAdapter      = "adapter"
	NLookup       = "lookup"
	NFetch        = "fetch"
	NBurst        = "burst"
	NNAT          = "nat"
	NTest         = "test"
	NSkipped      = "skipped"
	NPublicIP     = "public_ip"
	NPMTU         = "pmtu"
	NIdentity     = "identity"
	NWAN          = "wan"
	NWANReconnect = "wan_reconnect"
	NTunnel       = "tunnel"
	NDSL          = "dsl"
	NResync       = "resync"
	NDeviceLog    = "device_log"
	NWlanHistory  = "wlan_history"
	NWlanReport   = "wlanreport"
	NDriver       = "driver"
	NTCPRoute     = "tcp_route"
	NStart        = "start"
	NStop         = "stop"
	NDownsampled  = "downsampled"
)

// Fixed target labels.
const (
	TGateway  = "gateway"
	TGateway6 = "gateway6"
	TSystem   = "system"
)
