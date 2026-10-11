//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func chromeProfile() string {
	return filepath.Join(localAppData(), "Google", "Chrome", "User Data", "Default")
}

func TestCacheWhitelistKeepsProfileAndPersistentData(t *testing.T) {
	isolatedPaths(t)
	profile := chromeProfile()
	cache := filepath.Join(profile, "Cache")
	writeTestFile(t, filepath.Join(cache, "Cache_Data", "data_0"), "cache bytes")
	for _, path := range []string{
		filepath.Join(profile, "Cookies"), filepath.Join(profile, "Login Data"),
		filepath.Join(profile, "Preferences"), filepath.Join(profile, "Local Storage", "settings"),
		filepath.Join(profile, "IndexedDB", "messages"), filepath.Join(profile, "Extensions", "plugin"),
		filepath.Join(appDataDir(), "Code", "User", "settings.json"),
		filepath.Join(localAppData(), "UnknownApp", "Cache", "user.txt"),
	} {
		writeTestFile(t, path, "persistent")
	}
	entries := scanCache(1)
	if len(entries) != 1 || !samePath(entries[0].Path, cache) {
		t.Fatal("unexpected cache scan", entries)
	}
	for _, path := range []string{profile, filepath.Dir(profile), filepath.Join(profile, "Local Storage"),
		filepath.Join(profile, "IndexedDB"), filepath.Join(localAppData(), "UnknownApp", "Cache")} {
		if isCacheDeletable(path) {
			t.Fatal("persistent data accepted as cache", path)
		}
	}
	if _, err := deleteCacheJob([]string{cache}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(cache); err != nil || !info.IsDir() {
		t.Fatal("cache root must be preserved", err)
	}
	for _, path := range []string{filepath.Join(profile, "Cookies"), filepath.Join(profile, "Local Storage", "settings"),
		filepath.Join(profile, "IndexedDB", "messages"), filepath.Join(appDataDir(), "Code", "User", "settings.json")} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "persistent" {
			t.Fatal("persistent data changed", path, err)
		}
	}
}

func TestCacheWhitelistProfilesAndBatchValidation(t *testing.T) {
	isolatedPaths(t)
	base := filepath.Dir(chromeProfile())
	for _, name := range []string{"Default", "Profile 1", "Profile 12", "Profile secrets"} {
		writeTestFile(t, filepath.Join(base, name, "Cache", "data"), "cache")
	}
	entries := scanCache(1)
	if len(entries) != 3 {
		t.Fatal("invalid profile matched", entries)
	}
	valid := filepath.Join(base, "Default", "Cache")
	if _, err := deleteCacheJob([]string{valid, filepath.Join(base, "Profile secrets", "Cache")}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if _, err := os.Stat(filepath.Join(valid, "data")); err != nil {
		t.Fatal("valid part of rejected batch deleted", err)
	}
}

func TestCancelledScansReturnNoPartialResults(t *testing.T) {
	root := isolatedPaths(t)
	writeTestFile(t, filepath.Join(chromeProfile(), "Cache", "data"), "cache")
	writeTestFile(t, filepath.Join(userProfile(), "Videos", "clip.mp4"), "video")
	writeTestFile(t, filepath.Join(root, "large.bin"), "file")
	for _, task := range []string{"cache", "bigfiles", "media", "comms"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		control := &scanControl{ctx: ctx}
		var result interface{}
		var err error
		switch task {
		case "cache":
			result, err = scanCacheJob(control)
		case "bigfiles":
			result, err = scanBigFilesJob(control)
		case "media":
			result, err = scanMediaJob(control)
		case "comms":
			result, err = scanCommsJob(control)
		}
		if !errors.Is(err, context.Canceled) || result != nil {
			t.Fatal("cancelled scan returned partial results", task, result, err)
		}
	}
}

func TestScanProgressCanCancelDuringTraversal(t *testing.T) {
	isolatedPaths(t)
	for _, name := range []string{"one.mp4", "two.mp4"} {
		writeTestFile(t, filepath.Join(userProfile(), "Videos", name), "video")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := 0
	control := &scanControl{ctx: ctx, notify: func(progress ScanProgress) {
		updates++
		if progress.Visited < 1 || progress.Path == "" {
			t.Error("empty progress")
		}
		cancel()
	}}
	files, err := scanMediaControlled(control)
	if !errors.Is(err, context.Canceled) || len(files) != 0 || updates == 0 {
		t.Fatal("progress cancellation failed", files, err, updates)
	}
}

func TestGuiScanCancellationAndClose(t *testing.T) {
	app := &guiApp{}
	started, stopped := make(chan struct{}), make(chan error, 1)
	if _, err := app.asyncScan("media", func(control *scanControl) (interface{}, error) {
		close(started)
		<-control.ctx.Done()
		stopped <- control.check()
		return nil, control.check()
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := app.cancelScan("media"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker not cancelled")
	}
	app.close()
	if _, err := app.asyncScan("cache", func(*scanControl) (interface{}, error) { return nil, nil }); err == nil {
		t.Fatal("closed GUI accepted work")
	}
}

func TestAtomicSettingsFailurePreservesExistingFile(t *testing.T) {
	isolatedPaths(t)
	old := defaultSettings()
	if err := saveSettings(old); err != nil {
		t.Fatal(err)
	}
	name, _ := syscall.UTF16PtrFromString(settingsPath())
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	updated := old
	updated.CleanComms = true
	err = saveSettings(updated)
	_ = syscall.CloseHandle(handle)
	if err == nil {
		t.Fatal("replacing locked settings unexpectedly succeeded")
	}
	if actual := loadSettings(); !reflect.DeepEqual(old, actual) {
		t.Fatal("old settings damaged", actual)
	}
	temp, _ := filepath.Glob(filepath.Join(filepath.Dir(settingsPath()), ".settings-*.tmp"))
	if len(temp) != 0 {
		t.Fatal("temporary settings files left behind", temp)
	}
	if err := saveSettings(updated); err != nil {
		t.Fatal(err)
	}
	if !loadSettings().CleanComms {
		t.Fatal("valid update not persisted")
	}
}

func TestSettingsRejectInvalidRanges(t *testing.T) {
	isolatedPaths(t)
	settings := defaultSettings()
	settings.CountdownSec = 0
	if err := saveSettings(settings); err == nil {
		t.Fatal("invalid countdown accepted")
	}
	settings = defaultSettings()
	settings.CommsDays = 36501
	if err := saveSettings(settings); err == nil {
		t.Fatal("invalid age accepted")
	}
}

func TestEveryCacheRulePreservesAdjacentData(t *testing.T) {
	isolatedPaths(t)
	paths := map[string]bool{}
	for _, rule := range cacheRules() {
		pattern := rule.pattern
		pattern = strings.ReplaceAll(pattern, "Profile *", "Profile 1")
		pattern = strings.ReplaceAll(pattern, "*", "test.default")
		path := filepath.Join(rule.root, pattern)
		writeTestFile(t, filepath.Join(path, "data"), "cache")
		writeTestFile(t, filepath.Join(filepath.Dir(path), "Persistent.json"), "keep")
		paths[path] = true
		if !isCacheDeletable(path) {
			t.Fatal("valid rule not accepted", rule.software, path)
		}
	}
	entries := scanCache(1)
	if len(entries) != len(paths) {
		t.Fatal("cache coverage mismatch", len(entries), len(paths))
	}
	for path := range paths {
		if _, err := deleteCacheJob([]string{path}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "Persistent.json"))
		if err != nil || string(data) != "keep" {
			t.Fatal("adjacent persistent data changed", path, err)
		}
	}
}

func TestWindowsToolCannotBeReplacedThroughPath(t *testing.T) {
	isolatedPaths(t)
	t.Setenv("PATH", t.TempDir())
	cmd := windowsTool("compact.exe", "/?")
	if !pathWithin(cmd.Path, systemRoot()) || filepath.Base(cmd.Path) != "compact.exe" {
		t.Fatal("tool resolved outside Windows", cmd.Path)
	}
}

func TestAutoDryRunCLILeavesTemporaryAndCommsFilesUntouched(t *testing.T) {
	if os.Getenv("WDC_AUTO_DRY_HELPER") == "1" {
		flag.CommandLine = flag.NewFlagSet("wdc", flag.ExitOnError)
		os.Args = []string{"wdc", "--auto", "--dry-run", "--elevated", "--no-uac"}
		main()
		os.Exit(0)
	}
	isolatedPaths(t)
	settings := defaultSettings()
	settings.CleanComms = true
	if err := saveSettings(settings); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(userTemp(), "keep.txt")
	attachment := filepath.Join(userProfile(), "Documents", "WeChat Files", "account", "old.jpg")
	writeTestFile(t, temporary, "temporary")
	writeTestFile(t, attachment, "attachment")
	old := time.Now().AddDate(0, 0, -45)
	if err := os.Chtimes(attachment, old, old); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAutoDryRunCLILeavesTemporaryAndCommsFilesUntouched$")
	cmd.Env = append(os.Environ(), "WDC_AUTO_DRY_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("auto dry-run: %v\n%s", err, output)
	}
	for path, expected := range map[string]string{temporary: "temporary", attachment: "attachment"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != expected {
			t.Fatal("auto dry-run changed data", path, err)
		}
	}
}
