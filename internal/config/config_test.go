package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if c.Duration.Duration != 48*time.Hour {
		t.Fatalf("duration = %v", c.Duration)
	}
	th := c.Thresholds
	if th.LossPct != 1 || th.UDPLossPct != 3 || th.JitterMs != 30 || th.RTTP95Ms != 150 || th.MergeGap.Duration != 30*time.Second ||
		th.MarkWindow.Duration != 2*time.Minute || th.GatewayFloorMs != 30 || th.GatewayFactor != 5 ||
		th.RSSIWeakDBm != -70 || th.RxRatioWeak != 0.3 || th.RetryPct != 10 || th.OverlapAPs != 4 ||
		th.RouterUtil != 0.8 || th.LaptopShare != 0.2 || th.DNSSlowMs != 500 || th.RSSIStandingDBm != -67 {
		t.Fatalf("threshold defaults wrong: %+v", th)
	}
	if th.LikelyMinAwake.Duration != 30*time.Minute || th.LikelyMinBad.Duration != time.Minute || th.LikelyMinShare != 0.5 ||
		th.ConfidentMinAwake.Duration != 2*time.Hour || th.ConfidentMinBad.Duration != 3*time.Minute || th.ConfidentMinShare != 0.6 ||
		th.ConfidentMinIncidents != 2 || th.QuietMinAwake.Duration != 2*time.Hour || th.QuietMaxProblemPct != 0.5 {
		t.Fatalf("live verdict defaults wrong: %+v", th)
	}
	if len(c.Targets.IPv4) != 3 || len(c.Targets.IPv6) != 3 || len(c.STUN.Servers) != 2 || len(c.HTTP.Targets) != 6 {
		t.Fatalf("target defaults wrong: %+v", c)
	}
	if !c.Speedtest.Enabled || c.Speedtest.Interval.Duration != time.Hour || c.Speedtest.MaxDownloadMB != 25 || c.Speedtest.MaxUploadMB != 10 {
		t.Fatalf("speedtest defaults wrong: %+v", c.Speedtest)
	}
	if !c.Helper.Enabled || !c.UI.OpenBrowser || c.MaxDataMB != 200 {
		t.Fatalf("misc defaults wrong: %+v", c)
	}
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Duration.Duration != 48*time.Hour {
		t.Fatal("expected defaults")
	}
}

func TestLoadOverridesAndKeepsOtherDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "landlord.toml")
	content := `
duration = "24h"
[speedtest]
interval = "30m"
[thresholds]
loss_pct = 2.5
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Duration.Duration != 24*time.Hour || c.Speedtest.Interval.Duration != 30*time.Minute || c.Thresholds.LossPct != 2.5 {
		t.Fatalf("overrides not applied: %+v", c)
	}
	if c.Thresholds.JitterMs != 30 || c.Speedtest.MaxDownloadMB != 25 {
		t.Fatal("unrelated defaults lost")
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "landlord.toml")
	if err := os.WriteFile(p, []byte("durtion = \"1h\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "durtion") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}
