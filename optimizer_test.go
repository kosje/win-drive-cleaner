//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
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
