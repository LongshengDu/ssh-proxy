// Package supervisor owns the native SSH child, readiness, health checks, failover and the background run.
package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"ssh_proxy/src/bridge"
	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/logs"
	"ssh_proxy/src/platform"
)

type sshChild struct {
	cmd  *exec.Cmd
	job  func()
	done chan struct{}
	err  error
}

func startSSH(c *config.Config, alias string, logger *log.Logger) (*sshChild, error) {
	cmd, parentPipe, err := platform.ManagedCommand(config.SSHCommand(c, alias, false))
	if err != nil {
		return nil, err
	}
	platform.HideChild(cmd)
	cmd.WaitDelay = 3 * time.Second
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = writer
	child := &sshChild{cmd: cmd, done: make(chan struct{})}
	outputDone := make(chan struct{})
	var lastLine string
	go func() {
		defer close(outputDone)
		defer reader.Close()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			logger.Printf("SSH [%s] %s", alias, scanner.Text())
			lastLine = scanner.Text()
			if len(lastLine) > 4096 {
				lastLine = lastLine[:4096]
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		writer.Close()
		<-outputDone
		if parentPipe != nil {
			parentPipe.Close()
		}
		return nil, err
	}
	job, err := platform.ContainChild(cmd)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		writer.Close()
		<-outputDone
		if parentPipe != nil {
			parentPipe.Close()
		}
		return nil, fmt.Errorf("cannot contain SSH child: %w", err)
	}
	cleanup := func() {
		if parentPipe != nil {
			parentPipe.Close()
		}
		job()
	}
	child.job = cleanup
	go func() {
		child.err = cmd.Wait()
		writer.Close()
		<-outputDone // Drain diagnostics before publishing the exit to the supervisor.
		if child.err != nil && lastLine != "" {
			child.err = fmt.Errorf("%w: %s", child.err, lastLine)
		}
		close(child.done)
	}()
	return child, nil
}
func (c *sshChild) close() {
	platform.CloseJob(c.job)
	c.job = nil
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
}

type Supervisor struct {
	cfg    *config.Config
	paths  config.Paths
	logger *log.Logger
	bridge *bridge.Bridge
	mu     sync.Mutex
	status control.Status
}

func (s *Supervisor) snapshot() control.Status        { s.mu.Lock(); defer s.mu.Unlock(); return s.status }
func (s *Supervisor) update(fn func(*control.Status)) { s.mu.Lock(); fn(&s.status); s.mu.Unlock() }
func (s *Supervisor) probe(ctx context.Context) error {
	return bridge.ProbeTargets(ctx, s.bridge, s.cfg.Health.Targets, time.Duration(s.cfg.Health.Timeout)*time.Second)
}
func (s *Supervisor) awaitReady(ctx context.Context, child *sshChild) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.SSH.ReadyTimeout)*time.Second)
	defer cancel()
	var last error
	for {
		select {
		case <-child.done:
			return fmt.Errorf("SSH exited: %v", child.err)
		case <-ctx.Done():
			return fmt.Errorf("forwarding did not become ready: %v (%w)", last, ctx.Err())
		default:
		}
		if err := s.probe(ctx); err == nil {
			select {
			case <-child.done:
				return fmt.Errorf("SSH exited during readiness check: %v", child.err)
			default:
				return nil
			}
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
		case <-child.done:
		case <-time.After(300 * time.Millisecond):
		}
	}
}
func (s *Supervisor) monitor(ctx context.Context, child *sshChild) error {
	ticker := time.NewTicker(time.Duration(s.cfg.Health.Interval) * time.Second)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-child.done:
			return fmt.Errorf("SSH disconnected: %v", child.err)
		case <-ticker.C:
			if err := s.probe(ctx); err != nil {
				failures++
				s.logger.Printf("Health check failed (%d/%d): %v", failures, s.cfg.Health.Failures, err)
				s.update(func(st *control.Status) { st.Phase = control.PhaseDegraded; st.LastError = err.Error() })
				if s.cfg.Health.FailoverOnProbeFailure && failures >= s.cfg.Health.Failures {
					return fmt.Errorf("SSH forwarding health check failed: %w", err)
				}
			} else {
				failures = 0
				s.update(func(st *control.Status) { st.Phase = control.PhaseRunning; st.LastError = "" })
			}
		}
	}
}
func (s *Supervisor) supervise(ctx context.Context) {
	for ctx.Err() == nil {
		for _, endpoint := range s.cfg.Endpoints {
			if ctx.Err() != nil {
				return
			}
			s.update(func(st *control.Status) {
				st.Phase = control.PhaseConnecting
				st.Endpoint = endpoint
				st.Destination = s.cfg.Destination(endpoint)
				st.SSHPID = 0
			})
			s.logger.Printf("Connecting to %s (%s), SSH config: %s", endpoint, s.cfg.Destination(endpoint), s.cfg.SSH.ConfigFile)
			child, err := startSSH(s.cfg, endpoint, s.logger)
			if err == nil {
				s.update(func(st *control.Status) { st.SSHPID = child.cmd.Process.Pid })
				err = s.awaitReady(ctx, child)
				if err == nil {
					s.bridge.SetReady(true)
					s.update(func(st *control.Status) { st.Phase = control.PhaseRunning; st.LastError = "" })
					s.logger.Printf("Ready via %s: SOCKS5 %s; HTTP %s", endpoint, s.cfg.SocksAddr(), s.cfg.HTTPAddr())
					err = s.monitor(ctx, child)
				}
				s.bridge.SetReady(false)
				child.close()
			}
			if ctx.Err() != nil {
				return
			}
			s.update(func(st *control.Status) {
				st.LastError = err.Error()
				st.SSHPID = 0
				st.Phase = control.PhaseReconnecting
			})
			s.logger.Printf("Endpoint %s failed: %v; trying next endpoint", endpoint, err)
		}
		if ctx.Err() != nil {
			return
		}
		s.logger.Printf("All endpoints unavailable; retrying ordered list in %ds", s.cfg.SSH.RetryDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(s.cfg.SSH.RetryDelay) * time.Second):
		}
	}
}

// Run runs the supervisor in this process until it is stopped.
func Run(p config.Paths, foreground bool) error {
	return RunContext(context.Background(), p, foreground, false)
}

// RunContext also serves OS services, which mark their status as service-owned.
func RunContext(parent context.Context, p config.Paths, foreground, service bool) (runErr error) {
	var logger *log.Logger
	var writer *logs.RunLog
	var err error
	if inherited := os.Getenv("SSH_PROXY_RUN_LOG"); inherited != "" {
		logger, writer, err = logs.NewLog(inherited, foreground)
	} else {
		logger, writer, err = logs.NewRunLog(p, "logs", foreground)
	}
	if err != nil {
		return err
	}
	p.Log = writer.Name()
	defer writer.Close()
	defer func() {
		if runErr != nil {
			logger.Printf("Error: %v", runErr)
		}
	}()
	logger.Printf("Starting ssh_proxy_cli %s; pid=%d; config=%s", config.Version, os.Getpid(), p.Config)
	c, err := config.LoadConfig(p.Config)
	if err != nil {
		return err
	}
	writer.SetLimit(c.Logging.MaxSizeMB)
	unlock, err := platform.LockInstance(p.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if err = os.MkdirAll(p.Runtime, 0700); err != nil {
		return err
	}
	// Verify SOCKS is free before starting SSH. The HTTP listener remains reserved.
	check, err := net.Listen("tcp", c.SocksAddr())
	if err != nil {
		return fmt.Errorf("SOCKS port is unavailable: %w", err)
	}
	check.Close()
	httpListener, err := net.Listen("tcp", c.HTTPAddr())
	if err != nil {
		return fmt.Errorf("HTTP port is unavailable: %w", err)
	}
	defer httpListener.Close()
	controlListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer controlListener.Close()
	httpBridge, err := bridge.NewBridge(c.SocksAddr(), time.Duration(c.Health.Timeout)*time.Second)
	if err != nil {
		return err
	}
	defer httpBridge.SetReady(false)
	s := &Supervisor{cfg: c, paths: p, logger: logger, bridge: httpBridge, status: control.Status{Service: service, Phase: control.PhaseStarting, PID: os.Getpid(), Config: p.Config, Started: time.Now(), SOCKS: c.SocksAddr(), HTTP: c.HTTPAddr(), Log: p.Log}}
	ctx, stopSignal := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	state, err := control.NewState(p, controlListener.Addr().String())
	if err != nil {
		return err
	}
	controlServer := &http.Server{Handler: control.ControlHandler(state.Token, s.snapshot, cancel), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
	proxyServer := &http.Server{Handler: httpBridge, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second, ErrorLog: logger, BaseContext: func(net.Listener) context.Context { return ctx }}
	defer controlServer.Close()
	defer proxyServer.Close()
	go func() {
		if err := controlServer.Serve(controlListener); err != nil && err != http.ErrServerClosed {
			logger.Printf("Control server error: %v", err)
			cancel()
		}
	}()
	go func() {
		if err := proxyServer.Serve(httpListener); err != nil && err != http.ErrServerClosed {
			logger.Printf("HTTP server error: %v", err)
			cancel()
		}
	}()
	if err = control.WriteState(p, state); err != nil {
		return err
	}
	defer os.Remove(p.State)
	s.supervise(ctx)
	s.update(func(st *control.Status) { st.Phase = control.PhaseStopping; st.SSHPID = 0 })
	httpBridge.SetReady(false)
	logger.Print("Stopped; SSH child and proxy listeners closed")
	return nil
}

// StartBackground starts a detached _run supervisor and waits for readiness.
func StartBackground(p config.Paths, wait time.Duration) error {
	if _, err := control.Call(p, "GET", "/status"); err == nil {
		return nil
	}
	if err := os.MkdirAll(p.Runtime, 0700); err != nil {
		return err
	}
	_, file, err := logs.NewRunLog(p, "logs", false)
	if err != nil {
		return err
	}
	defer file.Close()
	p.Log = file.Name()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd, err := platform.StartDetached(func() *exec.Cmd {
		cmd := exec.Command(exe, "_run", "--config", p.Config)
		cmd.Env = append(os.Environ(), "SSH_PROXY_RUN_LOG="+p.Log)
		// Bootstrap output goes straight to the run log's file; the supervisor outlives this command.
		cmd.Stdout, cmd.Stderr = file.File, file.File
		return cmd
	})
	if err != nil {
		return err
	}
	pid := cmd.Process.Pid
	// Wait reaps an early exit. Polling the PID cannot see it on Unix, where an unreaped child stays a zombie.
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	deadline := time.Now().Add(wait)
	var last *control.Status
	for time.Now().Before(deadline) {
		if st, err := control.Call(p, "GET", "/status"); err == nil {
			last = &st
			if st.Phase == control.PhaseRunning {
				return nil
			}
		}
		select {
		case <-exited:
			data, _ := os.ReadFile(p.Log)
			return fmt.Errorf("supervisor exited during startup: %s\nLog: %s", stringsTrim(data), p.Log)
		case <-time.After(200 * time.Millisecond):
		}
	}
	if last != nil {
		return nil
	}
	return fmt.Errorf("supervisor pid %d has not exposed its control API; inspect %s", pid, p.Log)
}
func stringsTrim(data []byte) string {
	if len(data) == 0 {
		return "no bootstrap details"
	}
	return string(data)
}

// StopBackground asks the running supervisor to stop and waits for its lock.
func StopBackground(p config.Paths) error {
	if _, err := control.Call(p, "GET", "/status"); err != nil {
		// A stale state file alone is not authority to kill an arbitrary PID.
		if state, readErr := control.ReadState(p); readErr == nil && platform.ProcessAlive(state.PID) {
			return fmt.Errorf("control API unavailable for pid %d; no process was killed; inspect %s", state.PID, p.Log)
		}
		return nil
	}
	if _, err := control.Call(p, "POST", "/stop"); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if unlock, err := platform.LockInstance(p.ID); err == nil {
			unlock()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("shutdown is still in progress; inspect %s", p.Log)
}
