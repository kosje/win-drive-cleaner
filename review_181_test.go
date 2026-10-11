//go:build windows

package main

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestStartupApiSupportsRepeatedDisableAndRestore(t *testing.T) {
	isolatedPaths(t)
	original := filepath.Join(startupFolders()[StartupFolderUser], "测试启动项.lnk")
	writeTestFile(t, original, "shortcut")
	id := original
	for _, on := range []bool{false, true, false, true} {
		result, err := apiStartupToggle(id, on)
		if err != nil || result["ok"] != true {
			t.Fatal("startup toggle failed", result, err)
		}
		id = result["id"].(string)
		if id != result["target"] || id != startupFolderPath(original, on) {
			t.Fatal("stale startup identity", result)
		}
		if data, err := os.ReadFile(id); err != nil || string(data) != "shortcut" {
			t.Fatal("shortcut changed", id, err)
		}
	}
}

func TestStartupRenameNeverOverwritesConflictingFiles(t *testing.T) {
	isolatedPaths(t)
	original := filepath.Join(startupFolders()[StartupFolderUser], "app.lnk")
	disabled := original + startupDisabledSuffix
	writeTestFile(t, original, "original")
	writeTestFile(t, disabled, "previous-disabled")
	for _, attempt := range []struct {
		path string
		on   bool
	}{{original, false}, {disabled, true}} {
		result := setStartupFolderEnabled(StartupFolderUser, attempt.path, attempt.on)
		if result.OK {
			t.Fatal("conflicting target overwritten", result)
		}
	}
	for path, want := range map[string]string{original: "original", disabled: "previous-disabled"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatal("existing startup data lost", path, err)
		}
	}
}

func TestStartupDisabledSuffixIsCaseInsensitive(t *testing.T) {
	isolatedPaths(t)
	dir := startupFolders()[StartupFolderUser]
	writeTestFile(t, filepath.Join(dir, "app.lnk.WCD_DISABLED"), "shortcut")
	items := listStartupFolder(StartupFolderUser, dir)
	if len(items) != 1 || items[0].Enabled || items[0].Name != "app.lnk" {
		t.Fatal(items)
	}
	if result := setValueEnabled(&items[0], true); !result.OK {
		t.Fatal(result)
	}
	if items[0].ID != filepath.Join(dir, "app.lnk") || !items[0].Enabled {
		t.Fatal("restored identity not updated", items[0])
	}
}

func TestGuiElevationPreservesSelectedOptions(t *testing.T) {
	for _, deep := range []bool{false, true} {
		for _, dry := range []bool{false, true} {
			args := elevationArgs([]string{"--auto", "--dry-run", "--no-uac"}, guiElevationArgs(deep, dry))
			flags := flag.NewFlagSet("elevated", flag.ContinueOnError)
			gui := flags.Bool("gui", false, "")
			deepFlag := flags.Bool("deep", false, "")
			dryFlag := flags.Bool("dry-run", false, "")
			flags.Bool("elevated", false, "")
			if err := flags.Parse(args); err != nil || !*gui || *deepFlag != deep || *dryFlag != dry {
				t.Fatal("selection changed during elevation", args, err)
			}
			html := guiHTMLWithOptions(deep, dry)
			if strings.Contains(html, `value="deep" checked`) != deep || strings.Contains(html, `value="std" checked`) == deep || strings.Contains(html, `id="dryRun" checked`) != dry {
				t.Fatal("GUI did not initialize selected options", deep, dry)
			}
		}
	}
}

func TestEmptyLogDirectoryCanBeOpenedWithoutPriorDeletion(t *testing.T) {
	isolatedPaths(t)
	info, err := apiReadLog(10)
	if err != nil || info["dir"] != logDir() {
		t.Fatal(info, err)
	}
	if _, err := os.Stat(logDir()); !os.IsNotExist(err) {
		t.Fatal("log read unexpectedly wrote files", err)
	}
	if err := ensureLogDir(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(logDir()); err != nil || !info.IsDir() {
		t.Fatal("log directory not created", err)
	}
}

func TestLogTailPreservesBlankLinesAndNormalizesCRLF(t *testing.T) {
	for _, test := range []struct {
		data  string
		lines int
		want  string
		count int
	}{
		{"", 2, "", 0}, {"a\nb", 1, "b", 1}, {"a\r\nb\r\n", 2, "a\nb", 2},
		{"a\n\n", 1, "", 1}, {"a\nb\n\n", 2, "b\n", 2}, {"\nlast", 1, "last", 1},
		{"a\n\n\n", 2, "\n", 2}, {"a\nb\n", 0, "", 0},
		{strings.Repeat("旧记录汉字", 1000) + "\n最后一条\n\n", 2, "最后一条\n", 2},
	} {
		path := filepath.Join(t.TempDir(), "log.txt")
		writeTestFile(t, path, test.data)
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		content, count, err := readLogTail(file, test.lines)
		_ = file.Close()
		if err != nil || content != test.want || count != test.count {
			t.Fatalf("tail(%q,%d)=%q,%d,%v; want %q,%d", test.data[:min(len(test.data), 32)], test.lines, content, count, err, test.want, test.count)
		}
	}
}

func TestLogTailRejectsOversizeSingleLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	writeTestFile(t, path, strings.Repeat("x", (4<<20)+1)+"\n")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, _, err := readLogTail(file, 2); err == nil {
		t.Fatal("oversize log misleadingly reported empty")
	}
}

func TestSelfAutostartRestoresTaskManagerDisabledState(t *testing.T) {
	base := `Software\WinDriveCleanerReviewTest_` + strconv.FormatInt(time.Now().UnixNano(), 10)
	loc := regRunLoc{source: RegRunCU, hive: registry.CURRENT_USER, regPath: base + `\Run`, approved: base + `\Approved`}
	t.Cleanup(func() {
		_ = registry.DeleteKey(loc.hive, loc.approved)
		_ = registry.DeleteKey(loc.hive, loc.regPath)
		_ = registry.DeleteKey(loc.hive, base)
	})
	if err := registerSelfStartup(loc, false, ""); err != nil {
		t.Fatal("missing registration should be harmless", err)
	}
	command := `"C:\Program Files\Cleaner\cleaner.exe" --auto`
	if err := registerSelfStartup(loc, true, command); err != nil {
		t.Fatal(err)
	}
	if !selfStartupRegistered(loc) {
		t.Fatal("new registration not enabled")
	}
	if result := setRegEnabled(loc, runValueName, false); !result.OK {
		t.Fatal(result)
	}
	if selfStartupRegistered(loc) {
		t.Fatal("task-manager-disabled registration reported enabled")
	}
	if err := registerSelfStartup(loc, true, command); err != nil {
		t.Fatal(err)
	}
	if !selfStartupRegistered(loc) {
		t.Fatal("re-registration failed to clear disabled marker")
	}
	for i := 0; i < 2; i++ {
		if err := registerSelfStartup(loc, false, ""); err != nil {
			t.Fatal(err)
		}
	}
	if selfStartupRegistered(loc) {
		t.Fatal("deleted registration still enabled")
	}
}

func TestSelfAutostartRollsBackWhenApprovalFails(t *testing.T) {
	base := `Software\WinDriveCleanerReviewTest_` + strconv.FormatInt(time.Now().UnixNano(), 10)
	loc := regRunLoc{source: RegRunCU, hive: registry.CURRENT_USER, regPath: base + `\Run`, approved: base + "\x00invalid"}
	key, _, err := registry.CreateKey(loc.hive, loc.regPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	t.Cleanup(func() { _ = registry.DeleteKey(loc.hive, loc.regPath); _ = registry.DeleteKey(loc.hive, base) })
	previous := `%USERPROFILE%\old-cleaner.exe --auto`
	if err := key.SetExpandStringValue(runValueName, previous); err != nil {
		t.Fatal(err)
	}
	if err := registerSelfStartup(loc, true, `C:\new-cleaner.exe --auto`); err == nil {
		t.Fatal("invalid approval path accepted")
	}
	actual, kind, err := key.GetStringValue(runValueName)
	if err != nil || actual != previous || kind != registry.EXPAND_SZ {
		t.Fatal("previous registration not preserved", actual, kind, err)
	}
}
