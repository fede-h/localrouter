// Command localrouter is a smart HTTP reverse proxy in front of a remote
// Ollama instance. See README.md for the long form.
//
// Subcommands:
//
//	serve | watch         run the proxy in the foreground
//	start                 spawn the proxy in the background
//	stop | kill           stop the running proxy
//	restart               stop + start (carries forward listen/remote)
//	status                show daemon + remote state
//	list                  show configured + installed models
//	info <model>          print /api/show for a model
//	pull <model>          ask the remote to pull a model
//	use <model>           set the default model (and optionally start)
//	config                print resolved config paths and values
//	init-config           write or update config.json + models.list
//	version               print the version and exit
//
// Top-level flags on `serve` mirror the comeback_prompt.md contract:
//
//	--listen      bind address (default localhost:11434)
//	--remote      remote Ollama URL (required)
//	--auto-pull   auto-pull missing models on demand (default true)
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/fede-h/localrouter/internal/config"
	"github.com/fede-h/localrouter/internal/daemon"
	"github.com/fede-h/localrouter/internal/ollama"
	"github.com/fede-h/localrouter/internal/proxy"
)

// version is the build-time version string. Override with -ldflags.
var version = "0.2.0-dev"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("[localrouter] ")

	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "localrouter: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return cmdInteractive()
	}
	switch args[0] {
	case "serve", "watch":
		return cmdServe(args[1:])
	case "start":
		return cmdStart(args[1:])
	case "stop", "kill":
		return cmdStop()
	case "restart":
		return cmdRestart(args[1:])
	case "status":
		return cmdStatus()
	case "list":
		return cmdList(args[1:])
	case "info":
		return cmdInfo(args[1:])
	case "pull":
		return cmdPull(args[1:])
	case "use":
		return cmdUse(args[1:])
	case "config":
		return cmdConfig()
	case "init-config":
		return cmdInitConfig(args[1:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, `localrouter %s — HTTP reverse proxy for a remote Ollama.

Usage:
  localrouter <command> [flags]

Commands:
  serve, watch              run the proxy in the foreground
  start                     start the proxy in the background
  stop, kill                stop a running proxy
  restart                   stop + start
  status                    show proxy + remote state
  list [--installed]        show configured + installed models
  info <model>              /api/show for a model on the remote
  pull <model>              ask the remote to pull a model
  use <model> [--start]     set default model (optionally start the proxy)
  config                    show paths and resolved config
  init-config               write or update config + models.list
  version                   print version

Run "localrouter <command> --help" for command-specific flags.
`, version)
}

// loadPathsAndConfig is the prelude shared by most subcommands.
func loadPathsAndConfig() (config.Paths, config.Config, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return paths, config.Config{}, err
	}
	if err := paths.EnsureDirs(); err != nil {
		return paths, config.Config{}, err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return paths, cfg, err
	}
	return paths, cfg, nil
}

// requireRemote returns the remote URL from cfg, overridden by override, or
// errors with a friendly message if neither is set.
func requireRemote(cfg config.Config, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if cfg.RemoteURL != "" {
		return cfg.RemoteURL, nil
	}
	return "", errors.New("remote URL is not configured (set it with `localrouter init-config` or via --remote)")
}

// ---------------------------------------------------------------------------
// serve / watch
// ---------------------------------------------------------------------------

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "", "bind address (default from config or localhost:11434)")
	remote := fs.String("remote", "", "remote Ollama URL (default from config)")
	var autoPull tristateFlag
	fs.Var(&autoPull, "auto-pull", "auto-pull missing models on demand (true|false)")
	pullTimeout := fs.Duration("pull-timeout", 0, "max time for a single auto-pull (default from config, 30m)")
	noPID := fs.Bool("no-pidfile", false, "do not write a PID file (useful for systemd/Type=notify)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths, cfg, err := loadPathsAndConfig()
	if err != nil {
		return err
	}

	if *listen != "" {
		cfg.ListenAddr = *listen
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "localhost:11434"
	}

	remoteURL, err := requireRemote(cfg, *remote)
	if err != nil {
		return err
	}

	if autoPull.set {
		cfg.AutoPull = autoPull.value
	}

	pullDur := cfg.PullTimeout()
	if *pullTimeout > 0 {
		pullDur = *pullTimeout
	}

	if daemon.PortInUse(cfg.ListenAddr) {
		return fmt.Errorf("listen address %s is already in use", cfg.ListenAddr)
	}

	client, err := ollama.New(remoteURL)
	if err != nil {
		return err
	}

	srv, err := proxy.New(proxy.Options{
		Remote:      client,
		AutoPull:    cfg.AutoPull,
		PullTimeout: pullDur,
		Logger:      log.Default(),
	})
	if err != nil {
		return err
	}

	if !*noPID {
		if err := daemon.WritePID(paths.PIDFile); err != nil {
			log.Printf("warning: could not write pid file: %v", err)
		}
		defer func() {
			_ = daemon.RemovePID(paths.PIDFile)
		}()
	}

	st, _ := config.LoadState(paths)
	st.LastStartAt = time.Now().UTC()
	_ = config.SaveState(paths, st)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	err = srv.Run(ctx, cfg.ListenAddr)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// ---------------------------------------------------------------------------
// start (background)
// ---------------------------------------------------------------------------

func cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	listen := fs.String("listen", "", "bind address (default from config or localhost:11434)")
	remote := fs.String("remote", "", "remote Ollama URL (default from config)")
	var autoPull tristateFlag
	fs.Var(&autoPull, "auto-pull", "auto-pull missing models on demand (true|false)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths, cfg, err := loadPathsAndConfig()
	if err != nil {
		return err
	}
	if *listen == "" {
		*listen = cfg.ListenAddr
		if *listen == "" {
			*listen = "localhost:11434"
		}
	}
	resolvedRemote, err := requireRemote(cfg, *remote)
	if err != nil {
		return err
	}

	pid, _ := daemon.ReadPID(paths.PIDFile)
	if pid > 0 && daemon.ProcessAlive(pid) {
		return fmt.Errorf("localrouter is already running (pid %d); use `localrouter restart`", pid)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}

	childArgs := []string{"serve", "--listen", *listen, "--remote", resolvedRemote}
	if autoPull.set {
		childArgs = append(childArgs, fmt.Sprintf("--auto-pull=%v", autoPull.value))
	}

	childPID, err := daemon.Spawn(self, childArgs, paths.LogFile)
	if err != nil {
		return err
	}
	fmt.Printf("started localrouter (pid %d), logging to %s\n", childPID, paths.LogFile)

	time.Sleep(300 * time.Millisecond)
	if !daemon.ProcessAlive(childPID) {
		return fmt.Errorf("child process exited immediately, check %s", paths.LogFile)
	}
	return nil
}

// ---------------------------------------------------------------------------
// stop / kill
// ---------------------------------------------------------------------------

func cmdStop() error {
	paths, _, err := loadPathsAndConfig()
	if err != nil {
		return err
	}
	pid, err := daemon.ReadPID(paths.PIDFile)
	if err != nil {
		return err
	}
	if pid == 0 {
		fmt.Println("no PID file found; nothing to stop")
		return nil
	}
	if !daemon.ProcessAlive(pid) {
		fmt.Printf("pid %d is not alive; removing stale PID file\n", pid)
		return daemon.RemovePID(paths.PIDFile)
	}
	if err := daemon.Stop(pid, 5*time.Second); err != nil {
		return err
	}
	_ = daemon.RemovePID(paths.PIDFile)
	fmt.Printf("stopped localrouter (pid %d)\n", pid)
	return nil
}

// ---------------------------------------------------------------------------
// restart
// ---------------------------------------------------------------------------

func cmdRestart(args []string) error {
	if err := cmdStop(); err != nil {
		log.Printf("restart: stop step said: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	return cmdStart(args)
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func cmdStatus() error {
	paths, cfg, err := loadPathsAndConfig()
	if err != nil {
		return err
	}

	pid, _ := daemon.ReadPID(paths.PIDFile)
	procAlive := pid > 0 && daemon.ProcessAlive(pid)

	listen := cfg.ListenAddr
	if listen == "" {
		listen = "localhost:11434"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	healthOK, info, healthErr := daemon.Probe(ctx, listen, 2*time.Second)

	st, _ := config.LoadState(paths)

	fmt.Printf("localrouter %s\n", version)
	fmt.Printf("  config:       %s\n", paths.ConfigFile)
	fmt.Printf("  models list:  %s\n", paths.ModelsFile)
	fmt.Printf("  state:        %s\n", paths.StateFile)
	fmt.Printf("  log:          %s\n", paths.LogFile)
	fmt.Println()
	fmt.Printf("  listen:       %s\n", listen)
	fmt.Printf("  remote:       %s\n", cfgOrPlaceholder(cfg.RemoteURL))
	fmt.Printf("  auto-pull:    %v\n", cfg.AutoPull)
	fmt.Printf("  default model:%s\n", cfgOrPlaceholder(cfg.DefaultModel))
	fmt.Println()

	switch {
	case procAlive && healthOK:
		fmt.Printf("  proxy:        running (pid %d, health OK)\n", pid)
		fmt.Printf("    advertises: remote=%s auto_pull=%v\n", info.Remote, info.AutoPull)
	case procAlive && !healthOK:
		fmt.Printf("  proxy:        process pid %d running, but health probe failed: %v\n", pid, healthErr)
	case !procAlive && healthOK:
		fmt.Printf("  proxy:        health OK but PID file is stale or missing\n")
		fmt.Printf("    advertises: remote=%s auto_pull=%v\n", info.Remote, info.AutoPull)
	default:
		fmt.Println("  proxy:        not running")
	}

	if cfg.RemoteURL != "" {
		client, err := ollama.New(cfg.RemoteURL)
		if err == nil {
			reach := client.Reachable(cfg.ReachTimeout())
			fmt.Printf("  remote tcp:   %s\n", boolOK(reach))
		}
	}

	fmt.Println()
	if !st.LastStartAt.IsZero() {
		fmt.Printf("  last start:   %s\n", st.LastStartAt.Local().Format(time.RFC3339))
	}
	if !st.LastUseAt.IsZero() {
		fmt.Printf("  last use:     %s (model %q)\n", st.LastUseAt.Local().Format(time.RFC3339), st.TrackedModel)
	}
	if !st.LastPullAt.IsZero() {
		fmt.Printf("  last pull:    %s\n", st.LastPullAt.Local().Format(time.RFC3339))
	}
	return nil
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	installedOnly := fs.Bool("installed", false, "only show models currently installed on the remote")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths, cfg, err := loadPathsAndConfig()
	if err != nil {
		return err
	}

	curated, _ := config.LoadModels(paths)
	curatedSet := map[string]bool{}
	for _, m := range curated {
		curatedSet[m] = true
	}

	if cfg.RemoteURL == "" {
		if *installedOnly {
			return errors.New("--installed requires a configured remote; run `localrouter init-config`")
		}
		fmt.Println("Configured models (no remote configured — cannot check installed status):")
		for _, m := range curated {
			fmt.Printf("  %s\n", m)
		}
		return nil
	}

	client, err := ollama.New(cfg.RemoteURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tags, err := client.Tags(ctx)
	if err != nil {
		return fmt.Errorf("query remote /api/tags: %w", err)
	}
	installedSet := map[string]tagEntry{}
	for _, t := range tags {
		installedSet[t.Name] = tagEntry{Name: t.Name, Size: t.Size}
	}

	if *installedOnly {
		var names []string
		for n := range installedSet {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Printf("  %s\t%s\n", n, humanSize(installedSet[n].Size))
		}
		return nil
	}

	fmt.Println("Configured models (✓ installed):")
	for _, m := range curated {
		mark := " "
		if _, ok := installedSet[m]; ok {
			mark = "✓"
		} else if _, ok := installedSet[m+":latest"]; ok && !strings.Contains(m, ":") {
			mark = "✓"
		}
		size := ""
		if t, ok := installedSet[m]; ok {
			size = humanSize(t.Size)
		}
		fmt.Printf("  [%s] %-40s %s\n", mark, m, size)
	}

	var extras []string
	for n := range installedSet {
		if !curatedSet[n] {
			extras = append(extras, n)
		}
	}
	if len(extras) > 0 {
		sort.Strings(extras)
		fmt.Println("\nOther installed on remote:")
		for _, n := range extras {
			fmt.Printf("  %s\t%s\n", n, humanSize(installedSet[n].Size))
		}
	}
	return nil
}

type tagEntry struct {
	Name string
	Size int64
}

// ---------------------------------------------------------------------------
// info
// ---------------------------------------------------------------------------

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: localrouter info <model>")
	}
	_, cfg, err := loadPathsAndConfig()
	if err != nil {
		return err
	}
	remoteURL, err := requireRemote(cfg, "")
	if err != nil {
		return err
	}
	client, err := ollama.New(remoteURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	show, err := client.Show(ctx, rest[0])
	if err != nil {
		return err
	}
	if show.License != "" {
		fmt.Printf("License: (truncated %d chars)\n", len(show.License))
	}
	if show.System != "" {
		fmt.Printf("System prompt:\n  %s\n\n", trunc(show.System, 200))
	}
	if show.Template != "" {
		fmt.Printf("Template:\n  %s\n\n", trunc(show.Template, 200))
	}
	if show.Parameters != "" {
		fmt.Printf("Parameters:\n%s\n", indent(show.Parameters, "  "))
	}
	if len(show.Details) > 0 {
		fmt.Println("Details:")
		printMap(show.Details, "  ")
	}
	if len(show.ModelInfo) > 0 {
		fmt.Println("Model info:")
		printMap(show.ModelInfo, "  ")
	}
	return nil
}

// ---------------------------------------------------------------------------
// pull
// ---------------------------------------------------------------------------

func cmdPull(args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: localrouter pull <model>")
	}
	paths, cfg, err := loadPathsAndConfig()
	if err != nil {
		return err
	}
	remoteURL, err := requireRemote(cfg, "")
	if err != nil {
		return err
	}
	client, err := ollama.New(remoteURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.PullTimeout())
	defer cancel()
	if err := client.Pull(ctx, rest[0], func(msg string) {
		log.Printf("pull: %s", msg)
	}); err != nil {
		return err
	}
	st, _ := config.LoadState(paths)
	st.LastPullAt = time.Now().UTC()
	_ = config.SaveState(paths, st)
	fmt.Println("pull complete")
	return nil
}

// ---------------------------------------------------------------------------
// use
// ---------------------------------------------------------------------------

func cmdUse(args []string) error {
	fs := flag.NewFlagSet("use", flag.ContinueOnError)
	start := fs.Bool("start", false, "start the proxy in the background after setting the default")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: localrouter use <model> [--start]")
	}
	model := rest[0]

	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	// LoadFile (no env overrides) so we don't persist transient env vars.
	cfg, err := config.LoadFile(paths)
	if err != nil {
		return err
	}
	cfg.DefaultModel = model
	if err := config.Save(paths, cfg); err != nil {
		return err
	}
	st, _ := config.LoadState(paths)
	st.TrackedModel = model
	st.LastUseAt = time.Now().UTC()
	_ = config.SaveState(paths, st)

	fmt.Printf("default model set to %q\n", model)

	if *start {
		return cmdStart(nil)
	}
	return nil
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

func cmdConfig() error {
	paths, cfg, err := loadPathsAndConfig()
	if err != nil {
		return err
	}
	fmt.Printf("paths:\n")
	fmt.Printf("  config dir:   %s\n", paths.ConfigDir)
	fmt.Printf("  cache dir:    %s\n", paths.CacheDir)
	fmt.Printf("  config file:  %s\n", paths.ConfigFile)
	fmt.Printf("  models list:  %s\n", paths.ModelsFile)
	fmt.Printf("  state file:   %s\n", paths.StateFile)
	fmt.Printf("  pid file:     %s\n", paths.PIDFile)
	fmt.Printf("  log file:     %s\n", paths.LogFile)
	fmt.Println()
	fmt.Println("resolved config:")
	fmt.Printf("  listen_addr:        %q\n", cfg.ListenAddr)
	fmt.Printf("  remote_url:         %q\n", cfg.RemoteURL)
	fmt.Printf("  auto_pull:          %v\n", cfg.AutoPull)
	fmt.Printf("  pull_timeout_secs:  %d\n", cfg.PullTimeoutSecs)
	fmt.Printf("  reach_timeout_ms:   %d\n", cfg.ReachTimeoutMs)
	fmt.Printf("  default_model:      %q\n", cfg.DefaultModel)
	return nil
}

// ---------------------------------------------------------------------------
// init-config
// ---------------------------------------------------------------------------

func cmdInitConfig(args []string) error {
	fs := flag.NewFlagSet("init-config", flag.ContinueOnError)
	remote := fs.String("remote", "", "remote Ollama URL to persist")
	listen := fs.String("listen", "", "local listen address to persist")
	defaultModel := fs.String("default-model", "", "default model to persist")
	var autoPull tristateFlag
	fs.Var(&autoPull, "auto-pull", "persist auto-pull setting (true|false)")
	force := fs.Bool("force", false, "update an existing config file instead of erroring")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("usage: localrouter init-config [--remote URL] [--listen ADDR] [--default-model TAG] [--auto-pull=true|false] [--force]")
	}

	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}

	cfg := config.Defaults()
	existed := false
	if _, err := os.Stat(paths.ConfigFile); err == nil {
		existed = true
		if !*force {
			return fmt.Errorf("config already exists at %s — edit it directly, or rerun `localrouter init-config --force`", paths.ConfigFile)
		}
		cfg, err = config.LoadFile(paths)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat config: %w", err)
	}

	if *remote != "" {
		cfg.RemoteURL = *remote
	} else {
		cfg.RemoteURL = promptString(
			"Remote Ollama URL (e.g. http://192.168.1.50:11434)",
			cfg.RemoteURL,
		)
	}
	if cfg.RemoteURL == "" {
		fmt.Fprintln(os.Stderr, "no remote URL provided — you can edit "+paths.ConfigFile+" later.")
	}
	if *listen != "" {
		cfg.ListenAddr = *listen
	} else {
		cfg.ListenAddr = promptString("Local listen address", cfg.ListenAddr)
	}
	if *defaultModel != "" {
		cfg.DefaultModel = *defaultModel
	} else {
		cfg.DefaultModel = promptString("Default model (optional)", cfg.DefaultModel)
	}
	if autoPull.set {
		cfg.AutoPull = autoPull.value
	} else {
		cfg.AutoPull = promptBool("Auto-pull missing models on demand?", cfg.AutoPull)
	}

	if err := config.Save(paths, cfg); err != nil {
		return err
	}
	wrote, err := config.SeedModelsIfMissing(paths)
	if err != nil {
		return err
	}
	if existed {
		fmt.Printf("updated %s\n", paths.ConfigFile)
	} else {
		fmt.Printf("wrote %s\n", paths.ConfigFile)
	}
	if wrote {
		fmt.Printf("wrote starter %s (edit to taste)\n", paths.ModelsFile)
	}
	return nil
}

// ---------------------------------------------------------------------------
// interactive (no-args invocation)
// ---------------------------------------------------------------------------

func cmdInteractive() error {
	paths, cfg, err := loadPathsAndConfig()
	if err != nil {
		return err
	}
	if cfg.RemoteURL == "" {
		fmt.Println("No remote configured. Running `localrouter init-config` first.")
		return cmdInitConfig(nil)
	}

	pid, _ := daemon.ReadPID(paths.PIDFile)
	if pid > 0 && daemon.ProcessAlive(pid) {
		fmt.Printf("localrouter is already running (pid %d). Use `localrouter stop` or `localrouter restart`.\n\n", pid)
		return cmdStatus()
	}

	if !isInteractiveTTY() {
		printUsage(os.Stderr)
		return errors.New("no subcommand given and stdin is not a TTY; run with a subcommand")
	}

	curated, _ := config.LoadModels(paths)
	client, err := ollama.New(cfg.RemoteURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tags, _ := client.Tags(ctx)

	seen := map[string]bool{}
	var choices []string
	for _, m := range curated {
		if !seen[m] {
			choices = append(choices, m)
			seen[m] = true
		}
	}
	for _, t := range tags {
		if !seen[t.Name] {
			choices = append(choices, t.Name)
			seen[t.Name] = true
		}
	}
	if len(choices) == 0 {
		fmt.Println("No models configured or installed. Edit", paths.ModelsFile, "or run `localrouter pull <model>`.")
		return nil
	}

	fmt.Println("Pick a model:")
	for i, c := range choices {
		marker := "  "
		if c == cfg.DefaultModel {
			marker = "* "
		}
		fmt.Printf("%s[%d] %s\n", marker, i+1, c)
	}
	fmt.Print("Choice (Enter for default, q to quit): ")
	line := readLine()
	line = strings.TrimSpace(line)
	if line == "q" || line == "Q" {
		return nil
	}

	model := cfg.DefaultModel
	if line != "" {
		var idx int
		if _, err := fmt.Sscanf(line, "%d", &idx); err == nil && idx >= 1 && idx <= len(choices) {
			model = choices[idx-1]
		} else {
			return fmt.Errorf("invalid choice %q", line)
		}
	}
	if model == "" {
		return errors.New("no model selected and no default set")
	}

	cfg.DefaultModel = model
	_ = config.Save(paths, cfg)
	st, _ := config.LoadState(paths)
	st.TrackedModel = model
	st.LastUseAt = time.Now().UTC()
	_ = config.SaveState(paths, st)

	fmt.Printf("\nStarting proxy in the background with default model %q.\n", model)
	return cmdStart(nil)
}

// ---------------------------------------------------------------------------
// flag helpers / formatting
// ---------------------------------------------------------------------------

// tristateFlag is a bool flag that distinguishes "unset" from "false".
type tristateFlag struct {
	set   bool
	value bool
}

func (t *tristateFlag) String() string {
	if !t.set {
		return "<unset>"
	}
	return fmt.Sprintf("%v", t.value)
}

func (t *tristateFlag) Set(s string) error {
	switch strings.ToLower(s) {
	case "1", "true", "t", "yes", "y", "on":
		t.value = true
	case "0", "false", "f", "no", "n", "off":
		t.value = false
	default:
		return fmt.Errorf("invalid bool %q", s)
	}
	t.set = true
	return nil
}

func (t *tristateFlag) IsBoolFlag() bool { return true }

func cfgOrPlaceholder(s string) string {
	if s == "" {
		return "<unset>"
	}
	return s
}

func boolOK(b bool) string {
	if b {
		return "reachable"
	}
	return "unreachable"
}

func humanSize(n int64) string {
	if n <= 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTP"[exp])
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func printMap(m map[string]any, ind string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s%s: %v\n", ind, k, m[k])
	}
}

func promptString(label, def string) string {
	if !isInteractiveTTY() {
		return def
	}
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line := readLine()
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptBool(label string, def bool) bool {
	if !isInteractiveTTY() {
		return def
	}
	defStr := "y"
	if !def {
		defStr = "n"
	}
	fmt.Printf("%s [%s]: ", label, defStr)
	line := strings.TrimSpace(strings.ToLower(readLine()))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes" || line == "true" || line == "1"
}

func readLine() string {
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return line
}

func isInteractiveTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
