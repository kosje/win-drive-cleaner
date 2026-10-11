//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func isolatedPaths(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for key, path := range map[string]string{
		"SystemDrive": root, "SystemRoot": filepath.Join(root, "Windows"),
		"LOCALAPPDATA": filepath.Join(root, "Local"), "APPDATA": filepath.Join(root, "Roaming"),
		"USERPROFILE": filepath.Join(root, "User"), "ALLUSERSPROFILE": filepath.Join(root, "ProgramData"),
		"TEMP": filepath.Join(root, "Temp"), "TMP": filepath.Join(root, "Temp"),
	} {
		t.Setenv(key, path)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	oldDry, oldProgress := DryRun, OnProgress
	DryRun, OnProgress = false, nil
	ResetCleanCancel()
	t.Cleanup(func() { DryRun, OnProgress = oldDry, oldProgress; ResetCleanCancel() })
	return root
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSystemDriveFileIsDeletable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SystemDrive", filepath.VolumeName(root))
	file := filepath.Join(root, "video.mp4")
	writeTestFile(t, file, "video")
	if !isBigFileDeletable(file) || !isBigFileDeletable(strings.ToUpper(file)) {
		t.Fatal("ordinary files on the system drive must be accepted regardless of case")
	}
}

func TestBigFileProtectionAndScan(t *testing.T) {
	root := isolatedPaths(t)
	userFile := filepath.Join(root, "User", "large.bin")
	writeTestFile(t, userFile, strings.Repeat("x", 1<<20))
	for _, path := range []string{
		filepath.Join(root, "PAGEFILE.SYS"), filepath.Join(root, "pRoGrAm FiLeS", "app.bin"),
		filepath.Join(systemRoot(), "component.bin"),
	} {
		writeTestFile(t, path, strings.Repeat("x", 1<<20))
		if isBigFileDeletable(path) {
			t.Fatalf("accepted protected path: %s", path)
		}
	}
	files := scanBigFiles(1, 20)
	if len(files) != 1 || !samePath(files[0].Path, userFile) {
		t.Fatalf("scan must agree with deletion protection: %+v", files)
	}
	if pathWithin(filepath.Join(root+"-other", "file"), root) {
		t.Fatal("sibling with shared prefix accepted")
	}
}

func TestCleanPlanProtectsWindowsComponents(t *testing.T) {
	isolatedPaths(t)
	prefetchFound := false
	paths := map[string]bool{}
	for _, item := range collectItems(true) {
		key := strings.ToLower(filepath.Clean(item.Path))
		if paths[key] {
			t.Fatalf("duplicate cleaning target: %s", item.Path)
		}
		paths[key] = true
		if pathWithin(item.Path, filepath.Join(systemRoot(), "WinSxS")) ||
			pathWithin(item.Path, filepath.Join(systemRoot(), "System32")) {
			t.Fatalf("protected Windows target in cleaning plan: %+v", item)
		}
		if samePath(item.Path, filepath.Join(systemRoot(), "Prefetch")) {
			prefetchFound = item.Mode == ModeClearContents
		}
	}
	if !prefetchFound {
		t.Fatal("Prefetch must be under Windows and preserve its directory")
	}
}

func TestThumbnailCleaningPreservesOtherExplorerData(t *testing.T) {
	isolatedPaths(t)
	root := filepath.Join(localAppData(), "Microsoft", "Windows", "Explorer")
	writeTestFile(t, filepath.Join(root, "thumbcache_32.db"), "cache")
	writeTestFile(t, filepath.Join(root, "settings.bin"), "settings")
	writeTestFile(t, filepath.Join(root, "data", "keep.txt"), "keep")
	r := cleanItemWithDryRun(CleanItem{Path: root, Mode: ModeThumbnailCache}, false)
	if r.Bytes != 5 || r.Deleted != 1 || r.Errors != 0 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "settings.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "keep.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestPartialDeleteCountsOnlyRemovedBytes(t *testing.T) {
	root := isolatedPaths(t)
	target := filepath.Join(root, "cache")
	locked, unlocked := filepath.Join(target, "locked.bin"), filepath.Join(target, "free.bin")
	writeTestFile(t, locked, "locked-data")
	writeTestFile(t, unlocked, "free")
	ptr, _ := syscall.UTF16PtrFromString(locked)
	handle, err := syscall.CreateFile(ptr, syscall.GENERIC_READ, syscall.FILE_SHARE_READ,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	r := cleanItemWithDryRun(CleanItem{Path: target, Mode: ModeDeleteTree}, false)
	if r.Bytes != 4 || r.Deleted != 1 || r.Errors == 0 {
		t.Fatalf("partial deletion overcounted or hid errors: %+v", r)
	}
	if _, err := os.Stat(locked); err != nil {
		t.Fatal("locked file must remain", err)
	}
	if _, err := os.Stat(unlocked); !os.IsNotExist(err) {
		t.Fatal("unlocked file must be removed", err)
	}
}

func TestDryRunDoesNotLeakIntoExplicitDeletion(t *testing.T) {
	isolatedPaths(t)
	file := filepath.Join(userTemp(), "data.bin")
	writeTestFile(t, file, "12345")
	DryRun = true
	r := cleanItem(CleanItem{Path: file, Mode: ModeFileDelete})
	if r.Bytes != 5 {
		t.Fatalf("wrong dry-run estimate: %+v", r)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("dry run removed file", err)
	}
	result, err := deleteBigFilesJob([]string{file})
	if err != nil || result.(map[string]interface{})["deleted"] != 1 {
		t.Fatal("explicit deletion failed", result, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("explicit deletion inherited dry-run state", err)
	}
	logs, _ := filepath.Glob(filepath.Join(logDir(), "CleanerLog_*.txt"))
	if len(logs) != 1 {
		t.Fatal("real deletion must be logged even after dry run")
	}
}

func TestCacheScanKeepsSameSoftwareInBothRoots(t *testing.T) {
	isolatedPaths(t)
	for _, root := range cacheRoots() {
		writeTestFile(t, filepath.Join(root, "App", "data"), "123")
	}
	entries := scanCache(1)
	if len(entries) != 2 || samePath(entries[0].Path, entries[1].Path) {
		t.Fatalf("same software in Local and Roaming must both appear: %+v", entries)
	}
	for _, path := range []string{localAppData(), filepath.Join(localAppData(), "Microsoft"),
		filepath.Join(localAppData(), "App", "child"), filepath.Join(localAppData(), "nonexistent")} {
		if isCacheDeletable(path) {
			t.Fatalf("invalid cache path accepted: %s", path)
		}
	}
}

func TestDeleteBatchValidatesAllPathsBeforeDeleting(t *testing.T) {
	isolatedPaths(t)
	file := filepath.Join(userTemp(), "keep.bin")
	writeTestFile(t, file, "data")
	if _, err := deleteBigFilesJob([]string{file, systemRoot()}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("part of rejected batch was deleted", err)
	}
}

func TestDirectoryJunctionNeverTraversed(t *testing.T) {
	root := isolatedPaths(t)
	out := t.TempDir()
	outside := filepath.Join(out, "large.mp4")
	writeTestFile(t, outside, strings.Repeat("x", 1<<20))
	link := filepath.Join(localAppData(), "LinkedApp")
	// mklink only creates a junction; no shell-based deletion or moving.
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, out).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	if isCacheDeletable(link) || isBigFileDeletable(filepath.Join(link, "large.mp4")) {
		t.Fatal("junction target accepted for deletion")
	}
	if n, b := walkSize(link); n != 0 || b != 0 {
		t.Fatalf("walk followed junction: %d, %d", n, b)
	}
	res := cleanItemWithDryRun(CleanItem{Path: link, Mode: ModeClearContents}, false)
	if res.Errors == 0 {
		t.Fatal("junction root should be refused")
	}
	r := &Result{}
	r.deleteTree(root)
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("file outside cleanup root was touched", err)
	}
}

func TestStartupFolderDisableAndRestore(t *testing.T) {
	isolatedPaths(t)
	if !pathWithin(startupFolders()[StartupFolderUser], appDataDir()) {
		t.Fatal("user startup folder must be under Roaming")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "app.lnk")
	writeTestFile(t, file, "shortcut")
	if r := setStartupFolderEnabled(StartupFolderUser, file, false); !r.OK {
		t.Fatal(r)
	}
	entries := listStartupFolder(StartupFolderUser, dir)
	if len(entries) != 1 || entries[0].Enabled || entries[0].Name != "app.lnk" {
		t.Fatalf("disabled entry disappeared: %+v", entries)
	}
	if r := setValueEnabled(&entries[0], true); !r.OK {
		t.Fatal(r)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("restoration failed", err)
	}
}

func TestElevationErrorsAndArguments(t *testing.T) {
	for ret := uintptr(0); ret <= 32; ret++ {
		if shellExecuteError(ret, syscall.Errno(0)) == nil {
			t.Fatalf("ShellExecute error %d accepted as success", ret)
		}
	}
	if shellExecuteError(33, nil) != nil {
		t.Fatal("successful ShellExecute rejected")
	}
	args := elevationArgs([]string{"--gui", "--dry-run", "--no-uac=true", "--no-pause"}, []string{"--deep"})
	if strings.Join(args, " ") != "--no-pause --deep --elevated" {
		t.Fatalf("elevated task did not reflect interactive selection: %v", args)
	}
	quoted := quoteArgs([]string{"", `a b\`, `a"b`})
	if quoted != `"" "a b\\" a\"b` {
		t.Fatalf("invalid Windows argument escaping: %s", quoted)
	}
}

func TestExplorerTargetForMediaPaths(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"video.mp4", "video with spaces.mp4", "录屏, 中文.mp4"} {
		file := filepath.Join(root, "目标 文件夹,测试", name)
		writeTestFile(t, file, "video")
		path, directory, err := explorerTarget(file)
		if err != nil {
			t.Fatal(err)
		}
		if path != file || directory {
			t.Fatalf("media path was altered: %s, directory=%v", path, directory)
		}
	}
	t.Setenv("WDC_EXPLORER_TEST_DIR", root)
	path, directory, err := explorerTarget(`%WDC_EXPLORER_TEST_DIR%`)
	if err != nil || path != root || !directory {
		t.Fatal("Windows environment expansion failed", path, directory, err)
	}
	if _, _, err := explorerTarget(filepath.Join(root, "missing.mp4")); err == nil {
		t.Fatal("invalid media path accepted")
	}
}

func TestReadLogTail(t *testing.T) {
	isolatedPaths(t)
	path := filepath.Join(logDir(), "CleanerLog_20261011.txt")
	writeTestFile(t, path, "first\nsecond\nthird\n")
	r, err := apiReadLog(2)
	if err != nil || r["count"] != 2 || r["content"] != "second\nthird" {
		t.Fatalf("log tail loses a line at trailing newline: %+v, %v", r, err)
	}
}

func TestCleanCancellationBeforeFirstItem(t *testing.T) {
	isolatedPaths(t)
	file := filepath.Join(userTemp(), "keep.txt")
	writeTestFile(t, file, "keep")
	RequestCleanCancel()
	if results := runItemsWithOptions(false, false, nil); len(results) != 0 {
		t.Fatal("cancel request lost before worker started")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncMutationExclusionAndRecovery(t *testing.T) {
	app := &guiApp{}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	_, err := app.async("cacheDelete", func() (interface{}, error) {
		close(started)
		<-release
		defer close(done)
		panic("test recovery")
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := app.async("clean", func() (interface{}, error) { return nil, nil }); err == nil {
		t.Fatal("concurrent mutations accepted")
	}
	close(release)
	<-done
	deadline := time.Now().Add(time.Second)
	for {
		app.mu.Lock()
		busy := app.running["mutation"]
		app.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("panic left mutation guard stuck")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReadInputLineAndZeroDays(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, _ = file.WriteString("m1 m3\r\n0\n")
	_, _ = file.Seek(0, 0)
	old := os.Stdin
	os.Stdin = file
	defer func() { os.Stdin = old }()
	line, err := readInputLine()
	if err != nil || line != "m1 m3" {
		t.Fatal("space-separated choices were truncated", line, err)
	}
	if n := askInt("", 30); n != 0 {
		t.Fatal("0 days must remain read-only", n)
	}
}

// Optional desktop integration test: verify Explorer's actual folder and
// selection, then close only the window displaying this unique test folder.
func TestExplorerRevealsMediaIntegration(t *testing.T) {
	if os.Getenv("WDC_EXPLORER_INTEGRATION") != "1" {
		t.Skip("set WDC_EXPLORER_INTEGRATION=1 to verify on a Windows desktop")
	}
	file := filepath.Join(t.TempDir(), "录屏 文件夹,测试", "我的 录屏,2026.mp4")
	writeTestFile(t, file, "video")
	if err := OpenInExplorer(file); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WDC_MEDIA_TEST_PATH", file)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `
$expected = $env:WDC_MEDIA_TEST_PATH
$folder = Split-Path -LiteralPath $expected
$shell = New-Object -ComObject Shell.Application
for ($i = 0; $i -lt 100; $i++) {
  foreach ($window in $shell.Windows()) {
    try {
      if ($window.Document.Folder.Self.Path -eq $folder) {
        $items = $window.Document.SelectedItems()
        $selected = @()
        for ($j = 0; $j -lt $items.Count; $j++) { $selected += $items.Item($j).Path }
        if ($selected -contains $expected) { $window.Quit(); exit 0 }
      }
    } catch {}
  }
  Start-Sleep -Milliseconds 100
}
foreach ($window in $shell.Windows()) { try { Write-Output $window.Document.Folder.Self.Path } catch {} }
Write-Error 'Explorer did not reveal and select the expected media file'
exit 1`)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("desktop verification: %v\n%s", err, output)
	}
}

func TestMissingRegistryValueErrorsAreLanguageIndependent(t *testing.T) {
	if !isKeyNotValue(syscall.ERROR_FILE_NOT_FOUND) || isKeyNotValue(syscall.ERROR_ACCESS_DENIED) || isKeyNotValue(errors.New("other")) {
		t.Fatal("registry errors must use error codes, not English text")
	}
}

func TestRunOnceDoesNotPretendToDisableViaStartupApproved(t *testing.T) {
	for _, source := range []StartupSource{RegRunOnceCU, RegRunOnceLM} {
		loc, _ := resolveRegLoc(source)
		if result := setRegEnabled(loc, "test-value", false); result.OK {
			t.Fatal("unsupported RunOnce disable reported success")
		}
	}
}

func TestFreeSpaceReturnsAvailableBytesInsteadOfDiskCapacity(t *testing.T) {
	free, total := diskUsage()
	actual := freeSpaceOnSystemDrive()
	if free < 0 || total <= 0 || actual < 0 || actual > total {
		t.Fatalf("invalid disk usage: free=%d total=%d actual=%d", free, total, actual)
	}
	difference := actual - free
	if difference < 0 {
		difference = -difference
	}
	if difference > 64<<20 {
		t.Fatalf("CLI and GUI disagree on available space: %d vs %d (capacity %d)", actual, free, total)
	}
}
