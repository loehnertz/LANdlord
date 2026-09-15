package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/loehnertz/LANdlord/internal/app"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/platform/host"
	"github.com/loehnertz/LANdlord/internal/version"
)

const usage = `usage:
  landlord [run] [-config FILE] [-duration 48h] [-no-helper] [-no-browser] [-data-dir DIR] [-report-dir DIR]
  landlord simulate -scenario NAME [-out DIR] [-seed N]
  landlord report [-redact] [-out FILE] [-config FILE] SESSION_DIR
  landlord inspect SESSION_DIR
  landlord version`

func main() {
	if err := run(os.Args[1:]); err != nil {
		showError(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "", "run":
		if cmd == "run" {
			args = args[1:]
		}
		return record(args)
	case "--helper":
		return helper(args[1:])
	case "version":
		fmt.Println("landlord", version.String())
		return nil
	case "simulate":
		return simulate(args[1:])
	case "report":
		return rebuildReport(args[1:])
	case "inspect":
		if len(args) != 2 {
			return errors.New(usage)
		}
		return app.Inspect(os.Stdout, args[1])
	default:
		if len(cmd) > 0 && cmd[0] == '-' {
			return record(args)
		}
		return errors.New(usage)
	}
}

func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "landlord.toml"
	}
	return filepath.Join(filepath.Dir(exe), "landlord.toml")
}

func loadConfig(path string) (config.Config, error) {
	if path == "" {
		path = defaultConfigPath()
	}
	return config.Load(path)
}

func record(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "", "landlord.toml (default: next to the executable)")
	duration := fs.Duration("duration", 0, "how long to record (default 48h)")
	noHelper := fs.Bool("no-helper", false, "don't ask for admin rights")
	noBrowser := fs.Bool("no-browser", false, "don't open the status page or the report folder")
	dataDir := fs.String("data-dir", "", "where to keep measurement data")
	reportDir := fs.String("report-dir", "", "where to write the report (default: Documents/LANdlord)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *duration > 0 {
		cfg.Duration = config.Duration{Duration: *duration}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return app.Run(ctx, cfg, host.New(), app.RunOptions{NoHelper: *noHelper, NoBrowser: *noBrowser, DataDir: *dataDir, ReportDir: *reportDir})
}

func helper(args []string) error {
	fs := flag.NewFlagSet("helper", flag.ContinueOnError)
	session := fs.String("session", "", "session directory")
	parent := fs.Int("parent", 0, "process ID of the main LANdlord process")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *session == "" {
		return errors.New("--helper needs --session")
	}
	cfg, err := loadConfig("")
	if err != nil {
		cfg = config.Default()
	}
	return app.RunHelper(context.Background(), host.New(), app.HelperOptions{SessionDir: *session, ParentPID: *parent, MaxBytes: int64(cfg.MaxDataMB) << 20})
}

func simulate(args []string) error {
	fs := flag.NewFlagSet("simulate", flag.ContinueOnError)
	scenario := fs.String("scenario", "", "scenario name")
	out := fs.String("out", ".", "output directory")
	seed := fs.Int64("seed", 1, "random seed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := app.Simulate(*scenario, *out, *seed, time.Now())
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func rebuildReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	redact := fs.Bool("redact", false, "remove network names, addresses and logs")
	out := fs.String("out", "", "output file (default: SESSION_DIR/report.html)")
	configPath := fs.String("config", "", "landlord.toml with custom thresholds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New(usage)
	}
	dir := fs.Arg(0)
	cfg := config.Default()
	if *configPath != "" {
		var err error
		if cfg, err = config.Load(*configPath); err != nil {
			return err
		}
	}
	path := *out
	if path == "" {
		path = filepath.Join(dir, "report.html")
	}
	if err := app.RebuildReport(dir, path, *redact, cfg.Thresholds); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
