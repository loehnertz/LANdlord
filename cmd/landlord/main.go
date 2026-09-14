package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/loehnertz/LANdlord/internal/app"
	"github.com/loehnertz/LANdlord/internal/config"
	"github.com/loehnertz/LANdlord/internal/version"
)

const usage = `usage:
  landlord simulate -scenario NAME [-out DIR] [-seed N]
  landlord report [-redact] [-out FILE] [-config FILE] SESSION_DIR
  landlord version`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "landlord:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "version":
		fmt.Println("landlord", version.String())
		return nil
	case "simulate":
		return simulate(args[1:])
	case "report":
		return rebuildReport(args[1:])
	default:
		return errors.New(usage)
	}
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
