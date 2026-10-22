package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	kardianos "github.com/kardianos/service"
	"ssh_proxy/src/service"
)

func TestLinuxServiceUnit(t *testing.T) {
	unit, err := service.SystemdScript(&kardianos.Config{
		Executable: "/home/test/100% $proxy/ssh_proxy_cli", Arguments: []string{"_service"},
		WorkingDirectory: "/home/test/proxy folder", EnvVars: map[string]string{"SSH_PROXY_RUNTIME": "/home/test/runtime folder"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`ExecStart="/home/test/100%% $$proxy/ssh_proxy_cli" "_service"`,
		`Environment="SSH_PROXY_RUNTIME=/home/test/runtime folder"`, "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("missing %s:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "WorkingDirectory=") {
		t.Fatal("unit must use resolved paths without a quoted WorkingDirectory")
	}
}

func TestLinuxStartupReportsUserSystemdState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	id := "0123456789abcdef"
	name := "com.sshproxy." + id
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, tc := range []struct {
		output        string
		exit          int
		want, wantErr bool
	}{
		{"enabled", 0, true, false}, {"disabled", 1, false, false},
		{"masked", 1, false, false}, {"Failed to connect to bus", 1, false, true},
	} {
		script := "#!/bin/sh\n[ \"$1\" = --user ] || exit 99\n[ \"$2\" = is-enabled ] || exit 99\n[ \"$3\" = " + name + ".service ] || exit 99\nprintf '%s\\n' '" + tc.output + "'\n"
		if tc.exit != 0 {
			script += "exit 1\n"
		}
		if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		enabled, err := service.ServiceAutoStart(name, "status")
		if enabled != tc.want || (err != nil) != tc.wantErr {
			t.Fatalf("%s: enabled=%v err=%v", tc.output, enabled, err)
		}
	}
}
