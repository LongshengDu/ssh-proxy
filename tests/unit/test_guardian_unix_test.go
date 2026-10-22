//go:build darwin || linux

package tests

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/proxy"
)

func TestUnixGuardianHelper(t *testing.T) {
	if os.Getenv("SSH_PROXY_GUARDIAN_TEST") != "1" {
		t.Skip("subprocess only")
	}
	if err := proxy.Run([]string{"_ssh", os.Args[0], "-test.run=^TestUnixSSHHelper$"}); err == nil {
		t.Fatal("child should be cancelled after parent lease closes")
	}
}

func TestUnixSSHHelper(t *testing.T) {
	if os.Getenv("SSH_PROXY_GUARDIAN_TEST") != "1" {
		t.Skip("subprocess only")
	}
	fmt.Println(os.Getpid())
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
	var pid int
	select {
	case s := <-line:
		pid, err = strconv.Atoi(strings.TrimSpace(s))
		if err != nil || pid <= 0 {
			t.Fatalf("child pid: %q %v", s, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SSH child did not start")
	}
	defer func() {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
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
		if proxy.ProcessAlive(pid) {
			t.Fatal("SSH survived the parent lease closing")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("guardian did not stop")
	}
}
