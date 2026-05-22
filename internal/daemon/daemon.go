// Package daemon manages the localrouter background process: spawning it,
// tracking its PID, probing liveness, and stopping it cleanly.
//
// "Daemon" here is the colloquial sense — a long-running background
// process. We do not double-fork or otherwise emulate Unix daemonization;
// the launching shell stays the parent until it exits. For unattended use,
// run `localrouter watch` under tmux/nohup/systemd/Task Scheduler.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Status describes the result of probing for a running localrouter.
type Status struct {
	PID         int    `json:"pid"`
	Running     bool   `json:"running"`
	HealthOK    bool   `json:"health_ok"`
	HealthError string `json:"health_error,omitempty"`
	ListenAddr  string `json:"listen_addr,omitempty"`
	RemoteURL   string `json:"remote_url,omitempty"`
	AutoPull    bool   `json:"auto_pull,omitempty"`
}

// ReadPID returns the PID stored in path, or 0 if the file does not exist.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read pid: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}
	return pid, nil
}

// WritePID writes the current process's PID to path, with 0o600 perms.
func WritePID(path string) error {
	pid := os.Getpid()
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

// RemovePID deletes the PID file if it exists. Missing file is not an error.
func RemovePID(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ProcessAlive reports whether pid is a live process. On POSIX this uses
// the well-known signal-0 trick. On Windows os.FindProcess succeeds even
// for stale PIDs, so we fall back to "we cannot prove it is dead, assume
// alive" — the HTTP health probe is the authoritative answer there.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// FindProcess never returns an error on Windows; the only way to
		// be sure is to ask the kernel via OpenProcess, which is outside
		// the stdlib surface we want. Leave the call to the caller's
		// HTTP probe.
		return true
	}
	// POSIX: signal 0 returns nil if the process exists and we have
	// permission; ESRCH means dead, EPERM means alive-but-not-ours.
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "permission")
}

// Probe asks the running proxy whether it is healthy. listenAddr is the
// localrouter listen address, e.g. "localhost:11434".
func Probe(ctx context.Context, listenAddr string, timeout time.Duration) (healthOK bool, info ProbeInfo, err error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	// Build the URL; tolerate users who pass either "host:port" or full
	// "http://host:port" in the config.
	rawURL := listenAddr
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "http://" + rawURL
	}
	rawURL = strings.TrimRight(rawURL, "/") + "/__localrouter/healthz"

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, info, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, info, fmt.Errorf("health endpoint returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return false, info, fmt.Errorf("decode health: %w", err)
	}
	return info.OK, info, nil
}

// ProbeInfo mirrors the JSON returned by the proxy /__localrouter/healthz.
type ProbeInfo struct {
	OK       bool   `json:"ok"`
	Service  string `json:"service"`
	Remote   string `json:"remote"`
	AutoPull bool   `json:"auto_pull"`
}

// Spawn launches a detached child running the current binary with the given
// args. stdout and stderr are appended to logPath.
//
// On POSIX we put the child in a new session (via the per-OS helper below)
// so it survives parent-shell exit. On Windows there is no equivalent in
// pure stdlib; the child inherits the parent's console handles minus
// stdin/stdout/stderr, which is enough for `localrouter start` to work
// from any shell.
func Spawn(self string, args []string, logPath string) (int, error) {
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open log file: %w", err)
	}
	// We intentionally do NOT close logFile here; it stays inherited by
	// the child. The child will keep its own handle alive for its
	// lifetime; the parent's file descriptor is closed by Go when the
	// parent returns from Spawn (logFile goes out of scope and is GC'd).
	// To avoid the parent closing it prematurely, we duplicate the handle
	// into the cmd and let the OS clean it up.
	cmd := exec.Command(self, args...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return 0, fmt.Errorf("start child: %w", err)
	}
	// Capture PID before Release() — Release zeroes/invalidates Pid on
	// some platforms, so reading it afterwards gives us -1.
	pid := cmd.Process.Pid
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	_ = logFile.Close()
	return pid, nil
}

// Stop signals the process to exit. On POSIX we send os.Interrupt and wait
// up to timeout for it to die, then fall back to Kill. On Windows
// os.Interrupt isn't deliverable to detached processes through stdlib;
// Kill() is used directly.
func Stop(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if runtime.GOOS == "windows" {
		// No clean shutdown path through pure stdlib; TerminateProcess is
		// what Kill() resolves to.
		if err := proc.Kill(); err != nil {
			return fmt.Errorf("kill %d: %w", pid, err)
		}
		return nil
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		// Already dead is fine.
		if strings.Contains(strings.ToLower(err.Error()), "process already finished") {
			return nil
		}
		return fmt.Errorf("signal interrupt %d: %w", pid, err)
	}
	// Wait for exit. We can't Wait() because the process isn't our child;
	// poll signal-0 instead.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !ProcessAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := proc.Kill(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "process already finished") {
			return nil
		}
		return fmt.Errorf("force kill %d: %w", pid, err)
	}
	return nil
}

// PortInUse reports whether a TCP listener can be opened on addr. Used as a
// pre-flight before `start`/`watch` so we fail fast with a clear message
// instead of dropping into ListenAndServe.
func PortInUse(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

