package tests

import (
	"os"
	"path/filepath"
	"testing"

	"ssh_proxy/src/service"
)

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
