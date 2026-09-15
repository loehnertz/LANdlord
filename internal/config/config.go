// Package config holds LANdlord's settings: built-in defaults, optionally overridden by landlord.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Duration struct{ time.Duration }

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

type Thresholds struct {
	LossPct         float64  `toml:"loss_pct"`
	UDPLossPct      float64  `toml:"udp_loss_pct"`
	JitterMs        float64  `toml:"jitter_ms"`
	RTTP95Ms        float64  `toml:"rtt_p95_ms"`
	MergeGap        Duration `toml:"merge_gap"`
	MarkWindow      Duration `toml:"mark_window"`
	GatewayFloorMs  float64  `toml:"gateway_floor_ms"`
	GatewayFactor   float64  `toml:"gateway_factor"`
	RSSIWeakDBm     float64  `toml:"rssi_weak_dbm"`
	RxRatioWeak     float64  `toml:"rx_ratio_weak"`
	RetryPct        float64  `toml:"retry_pct"`
	OverlapAPs      int      `toml:"overlap_aps"`
	RouterUtil      float64  `toml:"router_util"`
	LaptopShare     float64  `toml:"laptop_share"`
	DNSSlowMs       float64  `toml:"dns_slow_ms"`
	RSSIStandingDBm float64  `toml:"rssi_standing_dbm"`

	// Live verdict readiness.
	LikelyMinAwake        Duration `toml:"likely_min_awake"`
	LikelyMinBad          Duration `toml:"likely_min_bad"`
	LikelyMinShare        float64  `toml:"likely_min_share"`
	ConfidentMinAwake     Duration `toml:"confident_min_awake"`
	ConfidentMinBad       Duration `toml:"confident_min_bad"`
	ConfidentMinShare     float64  `toml:"confident_min_share"`
	ConfidentMinIncidents int      `toml:"confident_min_incidents"`
	QuietMinAwake         Duration `toml:"quiet_min_awake"`
	QuietMaxProblemPct    float64  `toml:"quiet_max_problem_pct"`
}

type Config struct {
	Duration Duration `toml:"duration"`
	Targets  struct {
		IPv4 []string `toml:"ipv4"`
		IPv6 []string `toml:"ipv6"`
	} `toml:"targets"`
	STUN struct {
		Servers []string `toml:"servers"`
	} `toml:"stun"`
	HTTP struct {
		Targets []string `toml:"targets"`
	} `toml:"http"`
	DNS struct {
		Hosts []string `toml:"hosts"`
	} `toml:"dns"`
	Speedtest struct {
		Enabled       bool     `toml:"enabled"`
		Interval      Duration `toml:"interval"`
		MaxDownloadMB int      `toml:"max_download_mb"`
		MaxUploadMB   int      `toml:"max_upload_mb"`
	} `toml:"speedtest"`
	Thresholds Thresholds `toml:"thresholds"`
	Helper     struct {
		Enabled bool `toml:"enabled"`
	} `toml:"helper"`
	UI struct {
		OpenBrowser bool `toml:"open_browser"`
	} `toml:"ui"`
	DataDir   string `toml:"data_dir"`
	MaxDataMB int    `toml:"max_data_mb"`
}

func Default() Config {
	var c Config
	c.Duration = Duration{48 * time.Hour}
	c.Targets.IPv4 = []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}
	c.Targets.IPv6 = []string{"2606:4700:4700::1111", "2001:4860:4860::8888", "2620:fe::fe"}
	c.STUN.Servers = []string{"stun.l.google.com:19302", "stun.cloudflare.com:3478"}
	c.HTTP.Targets = []string{"zoom.us", "teams.microsoft.com", "meet.google.com", "www.youtube.com", "www.netflix.com", "www.google.com"}
	c.DNS.Hosts = []string{"www.google.com", "www.wikipedia.org", "www.microsoft.com", "www.netflix.com", "zoom.us"}
	c.Speedtest.Enabled = true
	c.Speedtest.Interval = Duration{time.Hour}
	c.Speedtest.MaxDownloadMB = 25
	c.Speedtest.MaxUploadMB = 10
	c.Thresholds = Thresholds{
		// Public STUN servers drop a few packets even on good lines, and calls tolerate some UDP loss.
		LossPct: 1, UDPLossPct: 3, JitterMs: 30, RTTP95Ms: 150,
		MergeGap: Duration{30 * time.Second}, MarkWindow: Duration{2 * time.Minute},
		GatewayFloorMs: 30, GatewayFactor: 5,
		RSSIWeakDBm: -70, RxRatioWeak: 0.3, RetryPct: 10, OverlapAPs: 4,
		RouterUtil: 0.8, LaptopShare: 0.2, DNSSlowMs: 500, RSSIStandingDBm: -67,
		LikelyMinAwake: Duration{30 * time.Minute}, LikelyMinBad: Duration{time.Minute}, LikelyMinShare: 0.5,
		ConfidentMinAwake: Duration{2 * time.Hour}, ConfidentMinBad: Duration{3 * time.Minute}, ConfidentMinShare: 0.6,
		ConfidentMinIncidents: 2,
		QuietMinAwake:         Duration{2 * time.Hour}, QuietMaxProblemPct: 0.5,
	}
	c.Helper.Enabled = true
	c.UI.OpenBrowser = true
	c.MaxDataMB = 200
	return c
}

// Load returns Default() overridden by the TOML file at path. A missing file is not an error.
func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	dec := toml.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return c, fmt.Errorf("%s: %s", path, strict.String())
		}
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}
