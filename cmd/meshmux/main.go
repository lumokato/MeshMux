package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/generator"
	"github.com/meshmux/meshmux/internal/publisher"
	"github.com/meshmux/meshmux/internal/runner"
	"github.com/meshmux/meshmux/internal/updater"
)

var version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "version":
		fmt.Println(version)
		return nil
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		overwrite := fs.Bool("force", false, "overwrite meshmux.local.json")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return config.InitLocal(*overwrite)
	case "generate":
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		target := commandArg(args[1:], "all")
		if target == "all" {
			written, err := generator.GenerateAll(cfg)
			if err != nil {
				return err
			}
			for _, path := range written {
				fmt.Println("wrote", path)
			}
			return nil
		}
		path, err := generator.GenerateNamed(cfg, target)
		if err != nil {
			return err
		}
		fmt.Println("wrote", path)
		return nil
	case "publish":
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		name := commandArg(args[1:], "")
		if name == "" {
			return fmt.Errorf("publish target is required")
		}
		target, ok := cfg.PublishTarget(name)
		if !ok {
			return fmt.Errorf("unknown publish target %q", name)
		}
		result, err := publisher.Publish(target)
		if err != nil {
			return err
		}
		fmt.Println("published", name)
		if result.URL != "" {
			fmt.Println("url:", result.URL)
		}
		if result.StatusCode != 0 {
			fmt.Println("status:", result.StatusCode)
		}
		fmt.Println("sha256:", result.SHA256)
		return nil
	case "download":
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		kind := commandArg(args[1:], "all")
		switch kind {
		case "mihomo":
			path, err := updater.Download(cfg.Components.Mihomo, "mihomo")
			if err != nil {
				return err
			}
			fmt.Println("installed mihomo:", path)
		case "dashboard":
			path, err := updater.Download(cfg.Components.Dashboard, "dashboard")
			if err != nil {
				return err
			}
			fmt.Println("installed dashboard:", path)
		case "all":
			path, err := updater.Download(cfg.Components.Mihomo, "mihomo")
			if err != nil {
				return err
			}
			fmt.Println("installed mihomo:", path)
			path, err = updater.Download(cfg.Components.Dashboard, "dashboard")
			if err != nil {
				return err
			}
			fmt.Println("installed dashboard:", path)
		default:
			return fmt.Errorf("download expects mihomo, dashboard, or all")
		}
		return nil
	case "test":
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		targetName := commandArg(args[1:], "windows")
		profile, err := generator.GenerateNamed(cfg, targetName)
		if err != nil {
			return err
		}
		return runner.TestConfig(cfg, profile)
	case "start":
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		targetName := commandArg(args[1:], "windows")
		profile, err := generator.GenerateNamed(cfg, targetName)
		if err != nil {
			return err
		}
		return runner.Start(cfg, profile)
	case "stop":
		return runner.Stop()
	case "restart":
		_ = runner.Stop()
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		targetName := commandArg(args[1:], "windows")
		profile, err := generator.GenerateNamed(cfg, targetName)
		if err != nil {
			return err
		}
		return runner.Start(cfg, profile)
	case "status":
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		fmt.Print(runner.Status(cfg))
		return nil
	case "dashboard":
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		return runner.Dashboard(cfg)
	case "proxy":
		cfg, _, err := load(args[1:])
		if err != nil {
			return err
		}
		mode := commandArg(args[1:], "show")
		return runner.Proxy(mode, cfg.Ports.Mixed)
	case "autostart":
		mode := commandArg(args[1:], "show")
		return runner.Autostart(mode)
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func load(args []string) (*config.Config, string, error) {
	cfg, path, err := config.Load(configPathArg(args))
	if err != nil {
		return nil, path, err
	}
	if err := enterRuntimeDir(path); err != nil {
		return nil, path, err
	}
	return cfg, path, nil
}

func enterRuntimeDir(configPath string) error {
	dataDir := config.LocalDataDir()
	if dataDir == "" {
		return nil
	}
	configAbs, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	configAbs = filepath.Clean(configAbs)
	dataAbs = filepath.Clean(dataAbs)
	if samePath(configAbs, filepath.Join(dataAbs, config.DefaultConfigPath)) || isPathInside(configAbs, dataAbs) {
		if err := os.MkdirAll(dataAbs, 0755); err != nil {
			return err
		}
		return os.Chdir(dataAbs)
	}
	return nil
}

func samePath(a, b string) bool {
	if strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) {
		return true
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func isPathInside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func commandArg(args []string, fallback string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--config" || arg == "-config" {
			i++
			continue
		}
		if arg == "--force" || arg == "-force" {
			continue
		}
		if strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "-config=") {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return fallback
}

func configPathArg(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--config" || arg == "-config" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if value, ok := strings.CutPrefix(arg, "--config="); ok {
			return value
		}
		if value, ok := strings.CutPrefix(arg, "-config="); ok {
			return value
		}
	}
	return ""
}

func usage() {
	fmt.Print(`MeshMux ` + version + `

Usage:
  meshmux init [-force]
  meshmux generate [target|all] [-config path]
  meshmux publish <publish-target> [-config path]
  meshmux download mihomo|dashboard|all [-config path]
  meshmux test [target] [-config path]
  meshmux start [target] [-config path]
  meshmux stop
  meshmux restart [target] [-config path]
  meshmux status [-config path]
  meshmux dashboard [-config path]
  meshmux proxy on|off|show [-config path]
  meshmux autostart on|off|show
  meshmux version
`)
}
