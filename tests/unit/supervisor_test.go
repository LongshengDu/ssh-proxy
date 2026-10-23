package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/cli"
	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/platform"
	"ssh_proxy/src/supervisor"
	"ssh_proxy/tests/helpers"
)

// Test subprocesses use the actual supervisor/guardian, with private Paths.
func runSupervisorProcess() {
	if len(os.Args) > 1 && (os.Args[1] == "_run" || os.Args[1] == "_ssh") {
		var err error
		if os.Args[1] == "_ssh" {
			err = cli.Run(os.Args[1:])
		} else {
			var p config.Paths
			if err = json.Unmarshal([]byte(os.Getenv("SSH_PROXY_TEST_PATHS")), &p); err == nil {
				if trigger := os.Getenv("SSH_PROXY_TEST_CRASH_FILE"); trigger != "" {
					go func() {
						for {
							if _, err := os.Stat(trigger); err == nil {
								panic("test background goroutine crash")
							}
							time.Sleep(10 * time.Millisecond)
						}
					}()
				}
				err = supervisor.Run(p, false)
			}
		}
		if errors.Is(err, platform.ErrAlreadyRunning) {
			os.Exit(platform.AlreadyRunningExitCode)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func TestFatalPanicReachesRunLog(t *testing.T) {
	p := supervisorPaths(t)
	trigger := filepath.Join(t.TempDir(), "crash")
	t.Setenv("SSH_PROXY_TEST_CRASH_FILE", trigger)
	if err := supervisor.StartBackground(p, time.Second); err != nil {
		t.Fatal(err)
	}
	st := control.QueryStatus(p)
	if err := os.WriteFile(trigger, nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && platform.ProcessAlive(st.PID) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := control.QueryStatus(p); got.Phase != control.PhaseStopped || !got.UncleanExit {
		t.Fatal(got)
	}
	data, err := os.ReadFile(st.Log)
	if err != nil || !strings.Contains(string(data), "panic: test background goroutine crash") || !strings.Contains(string(data), "goroutine") {
		t.Fatal("missing fatal crash stack", err, string(data))
	}
}

func supervisorPaths(t *testing.T) config.Paths {
	t.Helper()
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	p, err := config.PathsForExecutable(filepath.Join(t.TempDir(), "ssh_proxy_cli"))
	if err != nil {
		t.Fatal(err)
	}
	freePort := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	}
	text := strings.NewReplacer("socks_port = 1081", "socks_port = "+freePort(), "http_port = 8181", "http_port = "+freePort()).Replace(string(testutil.ConfigTemplate(t)))
	if err := os.WriteFile(p.Config, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(p.Config), "ssh_config"), []byte("Host test\n HostName 127.0.0.1\n Port 1\n User test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(p)
	t.Setenv("SSH_PROXY_TEST_PATHS", string(data))
	t.Cleanup(func() {
		if err := supervisor.StopBackground(p); err != nil {
			t.Error(err)
		}
	})
	return p
}

func waitSupervisor(t *testing.T, p config.Paths) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := control.Call(p, "GET", "/status"); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("supervisor did not publish status", control.QueryStatus(p))
}

func runLogCount(t *testing.T, p config.Paths) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(p.Logs, "logs_*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

func TestConcurrentStartsShareSupervisorAndLog(t *testing.T) {
	p := supervisorPaths(t)
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- supervisor.StartBackground(p, 2*time.Second) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if n := runLogCount(t, p); n != 1 {
		t.Fatal("expected one log", n)
	}
	before, _ := os.ReadFile(p.State)
	if err := supervisor.Run(p, true); !errors.Is(err, platform.ErrAlreadyRunning) {
		t.Fatal(err)
	}
	p.Log = ""
	_ = supervisor.RecordStartFailure(p, errors.New("invalid edited config"))
	after, _ := os.ReadFile(p.State)
	if string(before) != string(after) || runLogCount(t, p) != 1 {
		t.Fatal("duplicate start damaged live state/logs")
	}
	if err := supervisor.StopBackground(p); err != nil {
		t.Fatal(err)
	}
	if st := control.QueryStatus(p); st.Phase != control.PhaseStopped || st.LastError != "" {
		t.Fatal(st)
	}
}

func TestBackgroundStartJoinsForeground(t *testing.T) {
	p := supervisorPaths(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.RunContext(ctx, p, true) }()
	waitSupervisor(t, p)
	if err := supervisor.StartBackground(p, time.Second); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if runLogCount(t, p) != 1 {
		t.Fatal("join created another log")
	}
}

func TestSupervisorFailureDiagnostics(t *testing.T) {
	for _, mode := range []string{"config", "port", "parent", "panic", "server"} {
		t.Run(mode, func(t *testing.T) {
			p := supervisorPaths(t)
			var err error
			switch mode {
			case "config":
				os.WriteFile(p.Config, []byte("invalid = ["), 0600)
				err = supervisor.StartBackground(p, 2*time.Second)
			case "port":
				cfg, e := config.LoadConfig(p.Config)
				if e != nil {
					t.Fatal(e)
				}
				ln, e := net.Listen("tcp", cfg.HTTPAddr())
				if e != nil {
					t.Fatal(e)
				}
				defer ln.Close()
				err = supervisor.StartBackground(p, 2*time.Second)
			case "parent":
				err = supervisor.RecordStartFailure(p, errors.New("failed to launch child"))
			case "panic":
				//lint:ignore SA1012 Deliberately trigger supervisor panic recovery before state publication.
				err = supervisor.RunContext(nil, p, false) // signal context rejects a nil parent.
			case "server":
				ctx, cancel := context.WithCancelCause(context.Background())
				defer cancel(context.Canceled)
				done := make(chan error, 1)
				go func() { done <- supervisor.RunContext(ctx, p, false) }()
				waitSupervisor(t, p)
				cancel(errors.New("HTTP server: listener failed"))
				err = <-done
			}
			if err == nil {
				t.Fatal("failure succeeded")
			}
			st := control.QueryStatus(p)
			if st.Phase != control.PhaseStopped || !st.UncleanExit || st.LastError == "" || runLogCount(t, p) != 1 {
				t.Fatal(st, runLogCount(t, p), err)
			}
			data, e := os.ReadFile(st.Log)
			if e != nil || len(data) == 0 {
				t.Fatal(e, string(data))
			}
			if mode == "panic" && !strings.Contains(string(data), "runtime/debug.Stack") {
				t.Fatal("missing panic stack", string(data))
			}
			if err := supervisor.StopBackground(p); err != nil {
				t.Fatal("exit record must not be mistaken for a live PID", err)
			}
		})
	}
}

func TestDeadSupervisorReportsUncleanStop(t *testing.T) {
	p := supervisorPaths(t)
	if err := supervisor.StartBackground(p, time.Second); err != nil {
		t.Fatal(err)
	}
	st := control.QueryStatus(p)
	process, err := os.FindProcess(st.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	process.Release()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && platform.ProcessAlive(st.PID) {
		time.Sleep(20 * time.Millisecond)
	}
	dead := control.QueryStatus(p)
	if dead.Phase != control.PhaseStopped || !dead.UncleanExit || dead.Log != st.Log {
		t.Fatal(dead)
	}
	if err := supervisor.StartBackground(p, time.Second); err != nil {
		t.Fatal(err)
	}
	if st := control.QueryStatus(p); st.UncleanExit || st.Phase == control.PhaseStopped {
		t.Fatal(st)
	}
}
