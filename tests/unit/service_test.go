package tests

import (
	"ssh_proxy/src/service"
	"testing"
)

func TestLaunchdStartupStatus(t *testing.T) {
	for _, tc := range []struct {
		output  string
		enabled bool
	}{
		{`"com.sshproxy.test" => disabled`, false},
		{`"com.sshproxy.test" => enabled`, true},
		{`"com.sshproxy.test" => true`, false},
		{`"com.sshproxy.test" => false`, true},
		{`"com.sshproxy.other" => disabled`, true},
		{"", true},
	} {
		if got := service.LaunchdAutoStart("disabled services = {\n\t"+tc.output+"\n}", "com.sshproxy.test"); got != tc.enabled {
			t.Errorf("%s: enabled=%v", tc.output, got)
		}
	}
}
