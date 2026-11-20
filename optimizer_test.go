//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func decodePowerShellUTF16LE(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("utf16le payload must have even byte length, got %d", len(raw))
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	return string(utf16.Decode(u16))
}

func TestEncodePowerShellUTF16LE(t *testing.T) {
	original := "Write-Output '测试 UTF-16LE 编码!'"
	encoded := encodePowerShellUTF16LE(original)
	if decoded := decodePowerShellUTF16LE(t, encoded); decoded != original {
		t.Fatalf("round-trip mismatch: got %q want %q", decoded, original)
	}
}

func TestBuildOptimizerScriptsCoverAllFeatures(t *testing.T) {
	cases := []struct {
		action   string
		preset   string
		contains []string
	}{
		{
			action: OptActionPurgePinyin,
			preset: "1",
			contains: []string{
				`0804:00000409`,
				`81D4E9C9-1D3B-41BC-9E6C-4B40BF79E35E`,
				`FA550B04-5AD7-411F-A5AC-CA038EC515D7`,
				`SettingSync\Groups\Language`,
				`ReconcileLanguageResources`,
			},
		},
		{
			action: OptActionPurgePinyin,
			preset: "3",
			contains: []string{
				`$kbChoice = "3"`,
				`0804:00000409`,
				`0409:00000409`,
			},
		},
		{
			action: OptActionBlockUpdate,
			contains: []string{
				`"wuauserv"`, `"WaaSMedicSvc"`, `"UsoSvc"`, `"bits"`, `"dosvc"`,
				`NoAutoUpdate`, `SetDisableUXWUAccess`, `PerformRemediation`,
			},
		},
		{
			action: OptActionRemoveAI,
			contains: []string{
				`[1/22]`, `[22/22]`,
				`DisableAIDataAnalysis`, `TurnOffWindowsCopilot`, `DisableCocreator`,
				`DisableAIFabricService`, `DisableAiActions`, `DisableVoiceClarity`,
				`Disable-WindowsOptionalFeature`, `SettingsPageVisibility`, `EnableCowriter`,
			},
		},
		{
			action: OptActionRestorePinyin,
			contains: []string{
				`Keyboard Layout\Substitutes`, `Set-WinUserLanguageList`, `Enable-ScheduledTask`,
			},
		},
		{
			action: OptActionRestoreUpdate,
			contains: []string{
				`wuauserv`, `WaaSMedicSvc`, `UsoSvc`, `Start-Service`,
			},
		},
		{
			action: OptActionRestoreAI,
			contains: []string{
				`WindowsCopilot`, `WindowsAI`, `Enable-WindowsOptionalFeature`, `ShowCopilotButton`,
			},
		},
	}

	for _, tc := range cases {
		script, msg, err := buildOptimizerScript(tc.action, tc.preset)
		if err != nil {
			t.Fatalf("buildOptimizerScript(%q, %q) failed: %v", tc.action, tc.preset, err)
		}
		if msg == "" {
			t.Fatalf("expected non-empty success message for %q", tc.action)
		}
		for _, want := range tc.contains {
			if !strings.Contains(script, want) {
				t.Errorf("script for %q missing expected snippet %q", tc.action, want)
			}
		}
	}
}

func TestBuildOptimizerScriptValidation(t *testing.T) {
	if _, _, err := buildOptimizerScript(OptActionPurgePinyin, "9"); err == nil {
		t.Fatal("expected error for invalid pinyin preset")
	}
	if _, _, err := buildOptimizerScript("unknown-action", ""); err == nil {
		t.Fatal("expected error for unknown optimizer action")
	}
}

func TestOptimizeExecuteRequiresAdmin(t *testing.T) {
	origAdmin := optimizerAdminCheck
	defer func() { optimizerAdminCheck = origAdmin }()
	optimizerAdminCheck = func() bool { return false }

	if _, err := OptimizeExecute(OptActionRemoveAI, ""); err == nil || !strings.Contains(err.Error(), "管理员") {
		t.Fatalf("expected admin requirement error, got %v", err)
	}
}

func TestOptimizeExecuteValidatesBeforeAdminCheck(t *testing.T) {
	origAdmin := optimizerAdminCheck
	defer func() { optimizerAdminCheck = origAdmin }()
	optimizerAdminCheck = func() bool { return false }

	if _, err := OptimizeExecute("bogus", ""); err == nil || !strings.Contains(err.Error(), "未知") {
		t.Fatalf("expected unknown action error, got %v", err)
	}
	if _, err := OptimizeExecute(OptActionOptimizeAll, "7"); err == nil || !strings.Contains(err.Error(), "方案") {
		t.Fatalf("expected invalid preset error, got %v", err)
	}
}

// 真实调用系统 PowerShell，只执行 Write-Output，不修改系统。
// 校验：stdin 传输可承载超过命令行上限的长脚本，且中文输出不乱码。
func TestRunOptimizerPowerShellLongUnicodeScript(t *testing.T) {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	for b.Len() < 70000 {
		b.WriteString("# 填充注释用于验证超长脚本不会受 32767 字符命令行限制 0123456789\n")
	}
	b.WriteString("function Test-Joined { param([string]$A) return \"$A-完成\" }\n")
	b.WriteString("if ($true) {\n    Write-Output (Test-Joined '微软拼音')\n}\n")
	out, err := runOptimizerPowerShell(b.String())
	if err != nil {
		t.Fatalf("powershell failed: %v\n%s", err, out)
	}
	if out != "微软拼音-完成" {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestOptimizeExecuteAllContinuesAfterStageFailure(t *testing.T) {
	origAdmin, origRunner, origClean := optimizerAdminCheck, optimizerRunner, optimizerDeepClean
	defer func() {
		optimizerAdminCheck, optimizerRunner, optimizerDeepClean = origAdmin, origRunner, origClean
	}()
	optimizerAdminCheck = func() bool { return true }
	calls := 0
	optimizerRunner = func(string) (string, error) {
		calls++
		if calls == 1 {
			return "pinyin failed", errors.New("exit status 1")
		}
		return "ok", nil
	}
	cleaned := false
	optimizerDeepClean = func() (int, int, int64, int64) { cleaned = true; return 0, 0, 0, 0 }

	res, err := OptimizeExecute(OptActionOptimizeAll, "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 || !cleaned {
		t.Fatalf("all stages must still run after a failure: calls=%d cleaned=%v", calls, cleaned)
	}
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("expected ok=false when a stage failed")
	}
	if msg, _ := res["msg"].(string); !strings.Contains(msg, "微软拼音") {
		t.Fatalf("failure summary should name the failed stage, got %q", msg)
	}
}

func TestOptimizeExecuteAllRunsAllFourStages(t *testing.T) {
	origAdmin := optimizerAdminCheck
	origRunner := optimizerRunner
	origClean := optimizerDeepClean
	defer func() {
		optimizerAdminCheck = origAdmin
		optimizerRunner = origRunner
		optimizerDeepClean = origClean
	}()

	optimizerAdminCheck = func() bool { return true }
	var ranScripts []string
	optimizerRunner = func(script string) (string, error) {
		ranScripts = append(ranScripts, script)
		return "stage ok", nil
	}
	cleanCalled := false
	optimizerDeepClean = func() (int, int, int64, int64) {
		cleanCalled = true
		return 12, 0, 50 << 20, 48 << 20
	}

	res, err := OptimizeExecute(OptActionOptimizeAll, "2")
	if err != nil {
		t.Fatalf("OptimizeExecute(all) returned error: %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %#v", res)
	}
	if len(ranScripts) != 3 {
		t.Fatalf("expected 3 PowerShell stages before deep clean, got %d", len(ranScripts))
	}
	if !strings.Contains(ranScripts[0], `$kbChoice = "2"`) {
		t.Errorf("stage 1 should use preset 2")
	}
	if !strings.Contains(ranScripts[1], "WaaSMedicSvc") {
		t.Errorf("stage 2 should block Windows Update")
	}
	if !strings.Contains(ranScripts[2], "[22/22]") {
		t.Errorf("stage 3 should remove 22 Windows AI items")
	}
	if !cleanCalled {
		t.Errorf("stage 4 deep clean was not called")
	}
	out, _ := res["output"].(string)
	if !strings.Contains(out, "深度清理 C 盘垃圾完成") {
		t.Errorf("expected deep clean summary in output, got %q", out)
	}
}

func TestOptimizeExecutePropagatesRunnerFailure(t *testing.T) {
	origAdmin := optimizerAdminCheck
	origRunner := optimizerRunner
	defer func() {
		optimizerAdminCheck = origAdmin
		optimizerRunner = origRunner
	}()

	optimizerAdminCheck = func() bool { return true }
	optimizerRunner = func(string) (string, error) {
		return "partial output", errors.New("exit status 1")
	}

	res, err := OptimizeExecute(OptActionBlockUpdate, "")
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("expected ok=false on runner failure")
	}
	if out, _ := res["output"].(string); out != "partial output" {
		t.Fatalf("expected partial output preserved, got %q", out)
	}
}

// 用 PowerShell 自带解析器检查全部生成脚本的语法（只解析，不执行）。
func TestOptimizerScriptsParseInPowerShell(t *testing.T) {
	actions := []string{OptActionPurgePinyin, OptActionBlockUpdate, OptActionRemoveAI,
		OptActionRestorePinyin, OptActionRestoreUpdate, OptActionRestoreAI}
	for _, action := range actions {
		script, _, err := buildOptimizerScript(action, "3")
		if err != nil {
			t.Fatal(err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(script))
		checker := "$s = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + encoded + "'))\n" +
			"$tokens = $null; $errs = $null\n" +
			"[void][System.Management.Automation.Language.Parser]::ParseInput($s, [ref]$tokens, [ref]$errs)\n" +
			"if ($errs.Count) { $errs | ForEach-Object { Write-Output (\"L{0}: {1}\" -f $_.Extent.StartLineNumber, $_.Message) } } else { Write-Output 'PARSE-OK' }\n"
		out, err := runOptimizerPowerShell(checker)
		if err != nil || out != "PARSE-OK" {
			t.Errorf("script %q failed to parse: %v\n%s", action, err, out)
		}
	}
}

func TestThumbnailCacheCleansBothThumbAndIconCache(t *testing.T) {
	isolatedPaths(t)
	root := filepath.Join(localAppData(), "Microsoft", "Windows", "Explorer")
	writeTestFile(t, filepath.Join(root, "thumbcache_256.db"), "thumb")
	writeTestFile(t, filepath.Join(root, "iconcache_32.db"), "icon")
	writeTestFile(t, filepath.Join(root, "keep.db"), "keep")

	r := cleanItemWithDryRun(CleanItem{Path: root, Mode: ModeThumbnailCache}, false)
	if r.Deleted != 2 || r.Bytes != 9 || r.Errors != 0 {
		t.Fatalf("expected both thumbcache and iconcache deleted, got %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "keep.db")); err != nil {
		t.Fatalf("non-cache file should be preserved: %v", err)
	}
}

func TestAskYesNoConsumesFullLineWithTrailingSpaces(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _ = f.WriteString("n   \r\nq\r\n")
	_, _ = f.Seek(0, 0)
	oldStdin := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = oldStdin }()

	if askYesNo("confirm?", true) {
		t.Fatal("expected false for 'n   '")
	}
	next, err := readInputLine()
	if err != nil || next != "q" {
		t.Fatalf("trailing spaces in askYesNo leaked into next readInputLine: got %q, err=%v", next, err)
	}
}

func TestOptimizeCLIDryRunDoesNotRequireAdminOrRunScripts(t *testing.T) {
	origAdmin, origRunner, origClean := optimizerAdminCheck, optimizerRunner, optimizerDeepClean
	origInteractive, origNoPause := inInteractive, noPause
	defer func() {
		optimizerAdminCheck, optimizerRunner, optimizerDeepClean = origAdmin, origRunner, origClean
		inInteractive, noPause = origInteractive, origNoPause
	}()
	optimizerAdminCheck = func() bool { return false }
	ran := false
	optimizerRunner = func(string) (string, error) { ran = true; return "", nil }
	optimizerDeepClean = func() (int, int, int64, int64) { ran = true; return 0, 0, 0, 0 }
	inInteractive = true
	noPause = true

	RunOptimizeCLI(OptActionOptimizeAll, "2", true)
	if ran {
		t.Fatal("dry-run must not execute PowerShell scripts or deep clean")
	}
}
