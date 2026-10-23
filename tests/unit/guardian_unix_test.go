//go:build darwin || linux

package tests

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ssh-proxy/src/cli"
	"ssh-proxy/src/platform"
)

func TestUnixGuardianHelper(t *testing.T) {
	if os.Getenv("SSH_PROXY_GUARDIAN_TEST") != "1" {
		t.Skip("subprocess only")
	}
	if err := cli.Run([]string{"_ssh", os.Args[0], "-test.run=^TestUnixSSHHelper$"}); err == nil {
		t.Fatal("child should be cancelled after parent lease closes")
	}
}

func TestUnixSSHHelper(t *testing.T) {
	switch os.Getenv("SSH_PROXY_GUARDIAN_TEST") {
	case "1":
		// Like a ProxyCommand that SSH starts: stopping SSH must end it too.
		proxy := exec.Command(os.Args[0], "-test.run=^TestUnixSSHHelper$")
		proxy.Env = append(os.Environ(), "SSH_PROXY_GUARDIAN_TEST=proxy")
		if err := proxy.Start(); err != nil {
			t.Fatal(err)
		}
		fmt.Println(os.Getpid(), proxy.Process.Pid)
	case "proxy":
	default:
		t.Skip("subprocess only")
	}
	time.Sleep(time.Minute)
}

func TestUnixGuardianClosesSSHOnParentExit(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestUnixGuardianHelper$")
	cmd.Env = append(os.Environ(), "SSH_PROXY_GUARDIAN_TEST=1")
	lease, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	line := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(stdout).ReadString('\n'); line <- s }()
	var pids []int
	select {
	case s := <-line:
		for _, field := range strings.Fields(s) {
			if pid, err := strconv.Atoi(field); err == nil && pid > 0 {
				pids = append(pids, pid)
			}
		}
		if len(pids) != 2 {
			t.Fatalf("SSH and proxy pids: %q", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SSH child did not start")
	}
	defer func() {
		for _, pid := range pids {
			if p, err := os.FindProcess(pid); err == nil {
				p.Kill()
			}
		}
	}()
	lease.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("guardian did not stop")
	}
	// The orphaned proxy process is reaped by init shortly after it is killed.
	for deadline := time.Now().Add(5 * time.Second); platform.ProcessAlive(pids[0]) || platform.ProcessAlive(pids[1]); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("SSH or its proxy process survived the parent lease closing")
		}
	}
}

// Each proxied connection holds one ssh file, so SSH must not keep the caller's
// limit, which is 256 by default on macOS.
func TestUnixGuardianRaisesFileLimit(t *testing.T) {
	var files syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &files); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "_ssh", "/bin/sh", "-c", "ulimit -n")
	lease, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	out, err := cmd.Output()
	if want := strconv.FormatUint(min(files.Max, 1024), 10); err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("SSH file limit: %q %v; want %s", out, err, want)
	}
}
