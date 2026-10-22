package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func grantServiceControl(string) error { return nil }
func ServiceAutoStart(name, action string) (bool, error) {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if action != "status" {
		if out, err := exec.Command("/bin/launchctl", action, domain+"/"+name).CombinedOutput(); err != nil {
			return false, fmt.Errorf("launchctl %s: %w: %s", action, err, out)
		}
	}
	out, err := exec.Command("/bin/launchctl", "print-disabled", domain).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("launchctl: %w: %s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, `"`+name+`"`) && strings.Contains(line, "=> true") {
			return false, nil
		}
	}
	return true, nil
}
