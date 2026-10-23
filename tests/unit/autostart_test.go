package tests

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"ssh-proxy/src/platform"
)

func TestLoginEntryFormats(t *testing.T) {
	args := []string{`/home/test/100% $proxy & <folder>/ssh-proxy-cli`, "start", "--foreground"}
	entry := platform.LoginEntry{Name: "com.sshproxy.cli.test", Command: args}
	plist := entry.LaunchAgent()
	decoder := xml.NewDecoder(strings.NewReader(plist))
	var values []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if tag, ok := token.(xml.StartElement); ok && tag.Name.Local == "string" {
			var value string
			if err := decoder.DecodeElement(&value, &tag); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
	}
	if len(values) != 4 || values[1] != args[0] || !strings.Contains(plist, "<key>KeepAlive</key><false/>") {
		t.Fatal(plist, values)
	}
	if strings.Contains(plist, "AbandonProcessGroup") {
		t.Fatal("CLI should remain launchd-owned")
	}
	entry.AbandonProcessGroup = true
	if !strings.Contains(entry.LaunchAgent(), "<key>AbandonProcessGroup</key><true/>") {
		t.Fatal("app must preserve its independent children")
	}
	unit := platform.SystemdUnit(args)
	for _, want := range []string{`ExecStart="/home/test/100%% $$proxy & <folder>/ssh-proxy-cli" "start" "--foreground"`, "Restart=no", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Fatal("missing", want, unit)
		}
	}
}
