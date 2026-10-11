//go:build windows

package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func TestDeletionRejectsWin32PathAliases(t *testing.T) {
	isolatedPaths(t)
	protected := filepath.Join(systemRoot(), "component.bin")
	writeTestFile(t, protected, "protected")
	cache := filepath.Join(localAppData(), "Microsoft")
	writeTestFile(t, filepath.Join(cache, "settings.bin"), "settings")
	for _, suffix := range []string{".", " ", ". "} {
		alias := systemRoot() + suffix + `\component.bin`
		if isBigFileDeletable(alias) {
			t.Fatalf("protected Windows alias accepted: %s", alias)
		}
		if isCacheDeletable(cache + suffix) {
			t.Fatalf("protected AppData alias accepted: %s", cache+suffix)
		}
	}
	for _, path := range []string{protected + ":stream", `\\?\` + protected, `\\.\` + protected} {
		if isBigFileDeletable(path) {
			t.Fatalf("unsupported namespace/stream accepted: %s", path)
		}
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidAliasRejectsEntireDeletionBatch(t *testing.T) {
	isolatedPaths(t)
	file := filepath.Join(userTemp(), "keep.bin")
	writeTestFile(t, file, "keep")
	writeTestFile(t, filepath.Join(systemRoot(), "component.bin"), "system")
	if _, err := deleteBigFilesJob([]string{file, systemRoot() + `.\component.bin`}); err == nil {
		t.Fatal("aliased protected target accepted")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("partial deletion occurred", err)
	}
}

func TestShortAliasesRespectProtectedDirectories(t *testing.T) {
	root := isolatedPaths(t)
	protected := filepath.Join(root, "Program Files", "component.bin")
	writeTestFile(t, protected, "system")
	name, _ := windows.UTF16PtrFromString(protected)
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(name, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatal(err)
	}
	short := windows.UTF16ToString(buf[:n])
	if isBigFileDeletable(short) {
		t.Fatalf("protected 8.3 path accepted: %s", short)
	}
	resolved, err := resolvedPath(short)
	if err != nil || !samePath(resolved, protected) {
		t.Fatal("short-path resolution failed", resolved, err)
	}
}

func TestScanFromShortRootStillExcludesProtectedData(t *testing.T) {
	root := isolatedPaths(t)
	writeTestFile(t, filepath.Join(root, "User", "large.bin"), strings.Repeat("x", 1<<20))
	writeTestFile(t, filepath.Join(systemRoot(), "component.bin"), strings.Repeat("x", 1<<20))
	name, _ := windows.UTF16PtrFromString(root)
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(name, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SystemDrive", windows.UTF16ToString(buf[:n]))
	files := scanBigFiles(1, 20)
	if len(files) != 1 || !samePath(files[0].Path, filepath.Join(root, "User", "large.bin")) {
		t.Fatal("short-root scan bypassed protection", files)
	}
}

func TestTempMisconfigurationCannotWipeUserRoots(t *testing.T) {
	isolatedPaths(t)
	for _, root := range []string{userProfile(), localAppData(), appDataDir(), filepath.Join(userProfile(), "Documents")} {
		file := filepath.Join(root, "keep.txt")
		writeTestFile(t, file, "keep")
		t.Setenv("TEMP", root)
		r := cleanItemWithDryRun(CleanItem{Path: userTemp(), Mode: ModeClearContents}, false)
		if r.Errors == 0 || r.Bytes != 0 {
			t.Fatalf("unsafe TEMP accepted: %s: %+v", root, r)
		}
		if _, err := os.Stat(file); err != nil {
			t.Fatal("user data was deleted", err)
		}
	}
}

func TestBigFileScanReturnsBoundedAndStableTop(t *testing.T) {
	root := isolatedPaths(t)
	for i := 0; i < 12; i++ {
		file := filepath.Join(root, "Data", fmt.Sprintf("%02d.bin", i))
		writeTestFile(t, file, "")
		f, err := os.OpenFile(file, os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal(err)
		}
		err = f.Truncate(int64(1<<20 + i/2))
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	files := scanBigFiles(1, 3)
	if len(files) != 3 {
		t.Fatal(files)
	}
	for i, name := range []string{"10.bin", "11.bin", "08.bin"} {
		if filepath.Base(files[i].Path) != name {
			t.Fatalf("wrong Top 3 ordering: %+v", files)
		}
	}
}

func TestBigFileThresholdCannotOverflow(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("threshold beyond int64 bytes requires a 64-bit int")
	}
	root := isolatedPaths(t)
	writeTestFile(t, filepath.Join(root, "small.bin"), "small")
	threshold := int64(1 << 43)
	if files := scanBigFiles(int(threshold), 20); len(files) != 0 {
		t.Fatal("overflow caused small files to match", files)
	}
}

func TestLeftoverArchivePreservesPathsAndExcludesOldPacks(t *testing.T) {
	isolatedPaths(t)
	files := map[string]string{
		filepath.Join(systemRoot(), "Temp", "left", "same", "file.bin"):  "windows-left",
		filepath.Join(systemRoot(), "Temp", "right", "same", "file.bin"): "windows-right",
		filepath.Join(userTemp(), "left", "same", "file.bin"):            "user-left",
		filepath.Join(userTemp(), "right", "same", "file.bin"):           "user-right",
	}
	for path, data := range files {
		writeTestFile(t, path, data)
	}
	writeTestFile(t, filepath.Join(userTemp(), "CleanerPack_old.zip"), "old pack")
	seenNames := map[string]bool{}
	first, count, err := PackLeftovers()
	if err != nil || count != 4 {
		t.Fatal(first, count, err)
	}
	reader, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != 4 {
		t.Fatal("extra archive entries", len(reader.File))
	}
	for _, entry := range reader.File {
		if seenNames[entry.Name] || strings.HasPrefix(entry.Name, "/") || strings.Contains(entry.Name, "..") {
			t.Fatal("ambiguous archive entry", entry.Name)
		}
		seenNames[entry.Name] = true
		stream, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		root := userTemp()
		if strings.HasPrefix(entry.Name, "WindowsTemp/") {
			root = filepath.Join(systemRoot(), "Temp")
		}
		_, rel, _ := strings.Cut(entry.Name, "/")
		if string(data) != files[filepath.Join(root, filepath.FromSlash(rel))] {
			t.Fatal("archive content mismatch", entry.Name)
		}
	}
	second, count, err := PackLeftovers()
	if err != nil || count != 4 || second == first {
		t.Fatal("pack overwritten or recursively included", second, count, err)
	}
}

func TestOverlappingLeftoverRootsAreDeduplicated(t *testing.T) {
	isolatedPaths(t)
	root := filepath.Join(systemRoot(), "Temp")
	t.Setenv("TEMP", root)
	writeTestFile(t, filepath.Join(root, "one.bin"), "one")
	_, count, err := PackLeftovers()
	if err != nil || count != 1 {
		t.Fatal("overlapping roots archived twice", count, err)
	}
}

type immediateResultView struct {
	webview2.WebView
	onResult func()
}

func (w *immediateResultView) Dispatch(f func()) { f() }
func (w *immediateResultView) Eval(string)       { w.onResult() }

func TestAsyncReleasesTaskBeforeDeliveringResult(t *testing.T) {
	app := &guiApp{}
	result := make(chan error, 1)
	var first atomic.Bool
	app.w = &immediateResultView{onResult: func() {
		if first.CompareAndSwap(false, true) {
			_, err := app.async("readLog", func() (interface{}, error) { return nil, nil })
			result <- err
		}
	}}
	if _, err := app.async("readLog", func() (interface{}, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal("task remained busy after result delivery", err)
		}
	case <-time.After(time.Second):
		t.Fatal("task result was not delivered")
	}
}

func TestCompactSupportDetectionIsCached(t *testing.T) {
	old := compactSupported
	defer func() { compactSupported = old }()
	calls := 0
	compactSupported = sync.OnceValue(func() bool { calls++; return true })
	if !compactSupported() || !compactSupported() || calls != 1 {
		t.Fatal("compact support was repeatedly detected", calls)
	}
}

func TestCommsCleanupPreservesDatabaseAndSettings(t *testing.T) {
	root := isolatedPaths(t)
	root = filepath.Join(root, "Comms")
	names := []string{"old.jpg", "MSG.DB", "account.dat", "messages.sqlite3", "messages.db-wal", "settings.json", "key.pem"}
	old := time.Now().AddDate(0, 0, -45)
	for _, name := range names {
		path := filepath.Join(root, name)
		writeTestFile(t, path, "data")
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "recent.jpg"), "recent")
	target := CommsTarget{Name: "Test", Roots: []string{root}}
	bytes, count, found := commsSizeOlder(target, 30)
	if !found || bytes != 4 || count != 1 {
		t.Fatal("database included in cleanup estimate", bytes, count, found)
	}
	if _, count, _ := deleteCommsOlder(target, 0); count != 0 {
		t.Fatal("0-day read-only mode deleted files")
	}
	bytes, deleted, failures := deleteCommsOlder(target, 30)
	if bytes != 4 || deleted != 1 || failures != 0 {
		t.Fatal(bytes, deleted, failures)
	}
	for _, name := range append(names[1:], "recent.jpg") {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal("persistent data deleted", name, err)
		}
	}
}

func TestAutomaticCommsCleanupRequiresExplicitOptIn(t *testing.T) {
	isolatedPaths(t)
	if defaultSettings().CleanComms {
		t.Fatal("new users should not automatically delete chat attachments")
	}
	writeTestFile(t, settingsPath(), `{"enabled":true,"countdownSec":30,"commsDays":30}`)
	if loadSettings().CleanComms {
		t.Fatal("old settings must not imply consent to automatic comms deletion")
	}
	writeTestFile(t, settingsPath(), `{"enabled":true,"countdownSec":30,"commsDays":30,"cleanComms":true}`)
	if !loadSettings().CleanComms {
		t.Fatal("explicit opt-in was not loaded")
	}
}

func TestGuiVersionUsesTheCliVersion(t *testing.T) {
	old := compactSupported
	defer func() { compactSupported = old }()
	compactSupported = func() bool { return true }
	info, err := apiDiskInfo()
	if err != nil || info["version"] != version {
		t.Fatal("GUI and CLI versions disagree", info, err)
	}
}

func TestCountdownHonorsCancellationInFinalInterval(t *testing.T) {
	cancel := make(chan struct{}, 1)
	ticks := make(chan time.Time, 1)
	cancel <- struct{}{}
	ticks <- time.Now()
	if !waitCountdown(1, cancel, ticks) {
		t.Fatal("final-interval cancellation was lost")
	}
	if waitCountdown(0, make(chan struct{}), ticks) {
		t.Fatal("countdown cancelled without input")
	}
}

func TestCacheSelectionRejectsOverflowAndMalformedNumbers(t *testing.T) {
	got := parseInts("1, M3 m5，7; 0 -1 abc2 99999999999999999999999999999")
	if !reflect.DeepEqual(got, []int{1, 3, 5, 7}) {
		t.Fatal("invalid input selected unintended directories", got)
	}
}
