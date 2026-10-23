package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"ssh_proxy/src/config"
	"ssh_proxy/tests/helpers"
)

// templateInstall is an install folder with only the shipped support/*.in templates.
func templateInstall(t *testing.T) (dir, configPath string) {
	t.Helper()
	dir = t.TempDir()
	testutil.Support(t, dir)
	return dir, filepath.Join(dir, "config.toml")
}

// support/ is part of every install: without its templates, commands stop with a clear error.
func TestEnsureConfigNeedsSupportTemplates(t *testing.T) {
	dir, configPath := templateInstall(t)
	writeText(t, configPath, string(testutil.ConfigTemplate(t)))
	if err := os.Remove(filepath.Join(dir, "support", "config.toml.in")); err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureConfig(configPath); err == nil || !strings.Contains(err.Error(), "config.toml.in is missing") {
		t.Fatal("missing config.toml.in must fail", err)
	}
	if err := config.CopyIfMissing(filepath.Join(dir, "support", "absent.in"), filepath.Join(dir, "absent")); err == nil {
		t.Fatal("a missing template must fail when its file is missing")
	}
}

// A fresh install gets every support/NAME.in as NAME, and existing files are never replaced.
func TestEnsureConfigCopiesMissingFiles(t *testing.T) {
	dir, configPath := templateInstall(t)
	if err := config.EnsureConfig(configPath); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(dir, "support", "extra.in"), "extra file")
	if err := config.EnsureConfig(configPath); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.toml", "ssh_config", "extra"} {
		want, _ := os.ReadFile(filepath.Join(dir, "support", name+".in"))
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(got) != string(want) {
			t.Fatalf("%s not copied from its template: %v", name, err)
		}
	}
	writeText(t, filepath.Join(dir, "ssh_config"), "Host mine\n HostName mine.invalid\n")
	if err := config.EnsureConfig(configPath); err != nil {
		t.Fatal(err)
	}
	if got := readText(filepath.Join(dir, "ssh_config")); !strings.Contains(got, "Host mine") {
		t.Fatal("existing ssh_config was replaced", got)
	}
}

// After an update, config.toml keeps every entry it had, unused or invalid ones
// included, and gains new settings; an up-to-date file is left as written.
func TestEnsureConfigUpgradesSettings(t *testing.T) {
	dir, configPath := templateInstall(t)
	old := "# my comment\n[proxy]\nbind = \"127.0.0.1\"\nhttp_port = 9999\nhttp_prt = 9876\nobsolete = true\n\n[ssh]\nretry_delay_seconds = \"soon\"\n\n[logging]\n\n[retired]\nvalue = 1\n"
	writeText(t, configPath, old)
	if err := config.EnsureConfig(configPath); err != nil {
		t.Fatal(err)
	}
	var got, template map[string]any
	if _, err := toml.DecodeFile(configPath, &got); err != nil {
		t.Fatal(err)
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, "support", "config.toml.in"), &template); err != nil {
		t.Fatal(err)
	}
	proxy, _ := got["proxy"].(map[string]any)
	ssh, _ := got["ssh"].(map[string]any)
	retired, _ := got["retired"].(map[string]any)
	if proxy["http_port"] != int64(9999) || proxy["http_prt"] != int64(9876) || proxy["obsolete"] != true ||
		ssh["retry_delay_seconds"] != "soon" || retired["value"] != int64(1) {
		t.Fatalf("an existing entry was changed or removed: %v", got)
	}
	for table, settings := range template {
		values, _ := got[table].(map[string]any)
		for key := range settings.(map[string]any) {
			if _, ok := values[key]; !ok {
				t.Fatalf("new setting %s.%s not added", table, key)
			}
		}
	}
	// Up to date: not rewritten, so comments stay.
	current := strings.Replace(string(testutil.ConfigTemplate(t)), "http_port = 8181", "http_port = 9999", 1) + "\n[retired]\nvalue = 1\n"
	writeText(t, configPath, "# keep me\n"+current)
	if err := config.EnsureConfig(configPath); err != nil {
		t.Fatal(err)
	}
	if readText(configPath) != "# keep me\n"+current {
		t.Fatal("an up-to-date config was rewritten")
	}
	// Invalid TOML is left for validation to report.
	writeText(t, configPath, "proxy = [")
	if err := config.EnsureConfig(configPath); err != nil || readText(configPath) != "proxy = [" {
		t.Fatal("invalid TOML must be left untouched", err)
	}
}

// The tray and CLI autostart may upgrade the same config.toml at the same moment.
func TestEnsureConfigConcurrentUpgrades(t *testing.T) {
	dir, configPath := templateInstall(t)
	writeText(t, configPath, "[proxy]\nhttp_port = 9999\n")
	errs := make(chan error, 8)
	for range cap(errs) {
		go func() { errs <- config.EnsureConfig(configPath) }()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Fatal("concurrent upgrade failed:", err)
		}
	}
	var got map[string]any
	if _, err := toml.DecodeFile(configPath, &got); err != nil {
		t.Fatal(err)
	}
	if proxy, _ := got["proxy"].(map[string]any); proxy["http_port"] != int64(9999) || proxy["socks_port"] == nil {
		t.Fatalf("upgraded config: %v", got)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(leftovers) != 0 {
		t.Fatal("temporary files left behind:", leftovers)
	}
}

func writeText(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func readText(path string) string { data, _ := os.ReadFile(path); return string(data) }
