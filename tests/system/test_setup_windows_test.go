//go:build systemtest

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"ssh_proxy/tests/helpers"
)

// A logged trust session must leave native SSH's input/output on real console
// handles while native -E appends diagnostics to the same run log.
func TestTrustLoggingPreservesConsole(t *testing.T) {
	config, _ := testutil.Config(t, "Host first target\n HostName fixture.invalid\n User tester\n")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(testutil.ConfigTemplate(t)), `executable = "ssh"`, "executable = "+strconv.Quote(helper))
	if err := os.WriteFile(config, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	record, log := filepath.Join(t.TempDir(), "terminal.json"), filepath.Join(t.TempDir(), "trust.txt")
	t.Setenv("SSH_PROXY_TEST_PROCESS", "ssh")
	t.Setenv("SSH_PROXY_TEST_SSH_SUCCESS", "target")
	t.Setenv("SSH_PROXY_TEST_TERMINAL_RECORD", record)
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{testutil.CLIPath(testutil.SystemBuild(t)), "trust", "--log", log, "--config", config}))
	if err != nil {
		t.Fatal(err)
	}
	startup := windows.StartupInfo{Flags: windows.STARTF_USESHOWWINDOW, ShowWindow: windows.SW_HIDE}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var process windows.ProcessInformation
	if err := windows.CreateProcess(nil, line, nil, nil, false, windows.CREATE_NEW_CONSOLE, nil, nil, &startup, &process); err != nil {
		t.Fatal(err)
	}
	windows.CloseHandle(process.Thread)
	defer windows.CloseHandle(process.Process)
	state, err := windows.WaitForSingleObject(process.Process, 15000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		windows.TerminateProcess(process.Process, 1)
		t.Fatalf("trust did not finish: %d: %v", state, err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(process.Process, &code); err != nil || code != 0 {
		t.Fatalf("trust failed: %d: %v", code, err)
	}
	data, err := os.ReadFile(record)
	var terminal []bool
	if err != nil || json.Unmarshal(data, &terminal) != nil || len(terminal) != 3 || !terminal[0] || !terminal[1] || !terminal[2] {
		t.Fatalf("SSH must receive console stdin/stdout/stderr with --log: %s (%v)", data, err)
	}
	data, err = os.ReadFile(log)
	if err != nil || !strings.Contains(string(data), "SSH login for target") || !strings.Contains(string(data), "fixture SSH diagnostic") {
		t.Fatalf("setup diagnostics were not logged: %s (%v)", data, err)
	}
}
