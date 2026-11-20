//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unicode/utf16"
)

// 深度优化与净化模块（整合自 Win_Deep_Optimizer）：
// 1. 彻底删除微软拼音输入法并实施永久免疫（支持 3 种美式键盘保留方案）
// 2. 彻底封杀 Windows 系统更新（停用核心服务 / 破坏 WaaSMedic 自愈 / 策略拦截 / 禁用计划任务）
// 3. 彻底删除 Windows AI 全特性（对标 RemoveWindowsAI 22 项）
// 4. 一键终极精简（拼音免疫 + 封杀更新 + 删除 22 项 AI + 深度清理 C 盘垃圾）
// 5. 分类还原中心（恢复微软拼音 / 恢复 Windows 更新 / 恢复 22 项 AI 支持）

const (
	OptActionPurgePinyin   = "pinyin"
	OptActionBlockUpdate   = "block-update"
	OptActionRemoveAI      = "remove-ai"
	OptActionOptimizeAll   = "all"
	OptActionRestorePinyin = "restore-pinyin"
	OptActionRestoreUpdate = "restore-update"
	OptActionRestoreAI     = "restore-ai"
)

var (
	optimizerAdminCheck = isAdmin
	optimizerRunner     = runOptimizerPowerShell
	optimizerDeepClean  = runOptimizerDeepClean
)

// encodePowerShellUTF16LE 将脚本编码为 PowerShell -EncodedCommand 所需的 Base64 UTF-16LE。
func encodePowerShellUTF16LE(script string) string {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// runOptimizerPowerShell 调用系统目录中的 Windows PowerShell 静默执行脚本。
func runOptimizerPowerShell(script string) (string, error) {
	encoded := encodePowerShellUTF16LE(script)
	cmd := windowsTool(
		`WindowsPowerShell\v1.0\powershell.exe`,
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encoded,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runOptimizerDeepClean() (int, int, int64, int64) {
	freeBefore := freeSpaceOnSystemDrive()
	results := runItemsWithOptions(true, false, nil)
	deleted, errs, bytes := totalsOf(results)
	freeAfter := freeSpaceOnSystemDrive()
	var gain int64
	if freeBefore > 0 && freeAfter > 0 && freeAfter > freeBefore {
		gain = freeAfter - freeBefore
	}
	return deleted, errs, bytes, gain
}

func normalizePinyinPreset(preset string) (string, error) {
	p := strings.TrimSpace(preset)
	if p == "" {
		return "1", nil
	}
	if p != "1" && p != "2" && p != "3" {
		return "", fmt.Errorf("无效的美式键盘方案: %s（仅支持 1 / 2 / 3）", preset)
	}
	return p, nil
}

// buildOptimizerScript 根据操作类型生成对应的 PowerShell 脚本（纯函数，便于单测）。
func buildOptimizerScript(action, preset string) (string, string, error) {
	const header = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8
$ErrorActionPreference = 'Continue'

function Set-RegDword {
    param([string]$Path, [string]$Name, [int]$Value)
    try {
        if (!(Test-Path $Path)) { New-Item -Path $Path -Force -ErrorAction SilentlyContinue | Out-Null }
        Set-ItemProperty -Path $Path -Name $Name -Value $Value -Type DWord -Force -ErrorAction SilentlyContinue
    } catch {}
}

function Set-RegString {
    param([string]$Path, [string]$Name, [string]$Value)
    try {
        if (!(Test-Path $Path)) { New-Item -Path $Path -Force -ErrorAction SilentlyContinue | Out-Null }
        Set-ItemProperty -Path $Path -Name $Name -Value $Value -Type String -Force -ErrorAction SilentlyContinue
    } catch {}
}

function Remove-RegItemSafe {
    param([string]$Path, [string]$Name = $null)
    try {
        if (Test-Path $Path) {
            if ($Name) {
                Remove-ItemProperty -Path $Path -Name $Name -Force -ErrorAction SilentlyContinue
            } else {
                Remove-Item -Path $Path -Recurse -Force -ErrorAction SilentlyContinue
            }
        }
    } catch {}
}
`

	switch action {
	case OptActionPurgePinyin:
		p, err := normalizePinyinPreset(preset)
		if err != nil {
			return "", "", err
		}
		body := fmt.Sprintf(`
Write-Output ">>> 彻底删除微软拼音输入法并实施永久免疫（方案 %s）..."
$kbChoice = "%s"
$langList = Get-WinUserLanguageList
$zhLang = $langList | Where-Object { $_.LanguageTag -eq "zh-Hans-CN" }
if ($null -eq $zhLang) {
    $langList.Add("zh-Hans-CN")
    $zhLang = $langList | Where-Object { $_.LanguageTag -eq "zh-Hans-CN" }
}

$pinyin1 = "0804:{81D4E9C9-1D3B-41BC-9E6C-4B40BF79E35E}{FCEEBE02-D534-4D08-896A-770F5681D1B5}"
$pinyin2 = "0804:{FA550B04-5AD7-411F-A5AC-CA038EC515D7}{FCEEBE02-D534-4D08-896A-770F5681D1B5}"
$zhLang.InputMethodTips.Remove($pinyin1) | Out-Null
$zhLang.InputMethodTips.Remove($pinyin2) | Out-Null

if ($kbChoice -eq "1" -or $kbChoice -eq "3") {
    if (-not ($zhLang.InputMethodTips -contains "0804:00000409")) {
        $zhLang.InputMethodTips.Add("0804:00000409")
    }
    Set-RegString -Path "HKCU:\Keyboard Layout\Substitutes" -Name "00000804" -Value "00000409"
    Set-RegString -Path "Registry::HKEY_USERS\.DEFAULT\Keyboard Layout\Substitutes" -Name "00000804" -Value "00000409"
    Write-Output "  -> [√] 已配置并锁定【简体中文 - 美式键盘】(0804:00000409)"
}

if ($kbChoice -eq "2" -or $kbChoice -eq "3") {
    $enLang = $langList | Where-Object { $_.LanguageTag -eq "en-US" }
    if ($null -eq $enLang) {
        $newEn = New-WinUserLanguageList en-US
        $langList.Add($newEn[0])
        $enLang = $langList | Where-Object { $_.LanguageTag -eq "en-US" }
    }
    if (-not ($enLang.InputMethodTips -contains "0409:00000409")) {
        $enLang.InputMethodTips.Add("0409:00000409")
    }
    Write-Output "  -> [√] 已配置并添加【英语(美国) - 普通美式键盘】(0409:00000409)"
}

Set-WinUserLanguageList $langList -Force

$regProfile = "HKCU:\Control Panel\International\User Profile\zh-Hans-CN"
if (Test-Path $regProfile) {
    Get-ItemProperty $regProfile | Get-Member -MemberType NoteProperty | Where-Object {
        $_.Name -like "*81D4E9C9-1D3B-41BC-9E6C-4B40BF79E35E*" -or $_.Name -like "*FA550B04-5AD7-411F-A5AC-CA038EC515D7*"
    } | ForEach-Object {
        Remove-ItemProperty -Path $regProfile -Name $_.Name -Force -ErrorAction SilentlyContinue
    }
}

Set-RegDword -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\SettingSync\Groups\Language" -Name "Enabled" -Value 0
Disable-ScheduledTask -TaskPath "\Microsoft\Windows\LanguageComponentsInstaller\" -TaskName "Installation" -ErrorAction SilentlyContinue | Out-Null
Disable-ScheduledTask -TaskPath "\Microsoft\Windows\LanguageComponentsInstaller\" -TaskName "ReconcileLanguageResources" -ErrorAction SilentlyContinue | Out-Null

Write-Output "  -> [√] 微软账户语言同步与后台自动安装任务已阻断！"
Write-Output "  [√] 微软拼音已彻底清除并免疫，已保留所选美式键盘。"
`, p, p)
		return header + body, "微软拼音彻底删除与永久免疫完成", nil

	case OptActionBlockUpdate:
		body := `
Write-Output ">>> [1/3] 停止并锁定 Windows Update 与修复核心服务..."
$services = @("wuauserv", "WaaSMedicSvc", "UsoSvc", "bits", "dosvc")
foreach ($svc in $services) {
    Stop-Service -Name $svc -Force -ErrorAction SilentlyContinue
    Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\$svc" -Name "Start" -Value 4 -Type DWord -ErrorAction SilentlyContinue
}
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WaaSMedicSvc" -Name "FailureActions" -Value ([byte[]]@(0)) -ErrorAction SilentlyContinue
Write-Output "  -> 服务已全部禁用，WaaSMedic 自愈更新机制已被破坏！"

Write-Output ">>> [2/3] 注入组策略规则：彻底屏蔽更新自动检测..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU" -Name "NoAutoUpdate" -Value 1
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU" -Name "AUOptions" -Value 2
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate" -Name "SetDisableUXWUAccess" -Value 1
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate" -Name "DoNotConnectToWindowsUpdateInternetLocations" -Value 1
Write-Output "  -> 策略拦截已生效！"

Write-Output ">>> [3/3] 禁用所有 Update 计划唤醒任务..."
$tasks = @(
    "\Microsoft\Windows\UpdateOrchestrator\Schedule Scan",
    "\Microsoft\Windows\UpdateOrchestrator\Schedule Scan Static Task",
    "\Microsoft\Windows\UpdateOrchestrator\UpdateModelTask",
    "\Microsoft\Windows\WaaSMedic\PerformRemediation",
    "\Microsoft\Windows\WindowsUpdate\Scheduled Start"
)
foreach ($t in $tasks) {
    $taskPath = $t.Substring(0, $t.LastIndexOf('\') + 1)
    $taskName = $t.Substring($t.LastIndexOf('\') + 1)
    Disable-ScheduledTask -TaskPath $taskPath -TaskName $taskName -ErrorAction SilentlyContinue | Out-Null
}
Write-Output "  [√] Windows 系统更新已被完全封杀！"
`
		return header + body, "Windows 系统更新已彻底封杀", nil

	case OptActionRemoveAI:
		body := `
Write-Output ">>> 正在执行对标 RemoveWindowsAI 的 22 项全维度深度清理..."

Write-Output "  [1/22] 强杀常驻后台 AI 进程以释放内存..."
@("Copilot", "WindowsCopilot", "AIFabricService", "Recall", "CoreAI") | ForEach-Object {
    Stop-Process -Name $_ -Force -ErrorAction SilentlyContinue
}

Write-Output "  [2/22] 写入 WindowsAI 核心禁用注册表..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsAI" -Name "DisableAIDataAnalysis" -Value 1
Set-RegDword -Path "HKCU:\Software\Policies\Microsoft\Windows\WindowsAI" -Name "DisableAIDataAnalysis" -Value 1

Write-Output "  [3/22] 关闭 Copilot 系统开关并隐藏任务栏图标..."
Set-RegDword -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" -Name "ShowCopilotButton" -Value 0

Write-Output "  [4/22] 禁用 Windows Recall 快照抓取与索引..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsAI" -Name "DisableAIDataAnalysis" -Value 1

Write-Output "  [5/22] 关闭输入洞察与打字数据收集功能..."
Set-RegDword -Path "HKCU:\Software\Microsoft\Input\Settings" -Name "InsightsEnabled" -Value 0
Set-RegDword -Path "HKCU:\Software\Microsoft\Personalization\Settings" -Name "AcceptedPrivacyPolicy" -Value 0
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\InputPersonalization" -Name "AllowInputPersonalization" -Value 0
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\InputPersonalization" -Name "RestrictImplicitInkCollection" -Value 1
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\InputPersonalization" -Name "RestrictImplicitTextCollection" -Value 1

Write-Output "  [6/22] 禁用 Edge 侧边栏 Copilot (释放后台预热的 msedgewebview2)..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Edge" -Name "HubsSidebarEnabled" -Value 0
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Edge" -Name "ShowCopilotButton" -Value 0

Write-Output "  [7/22] 禁用画图工具中的图像创建器 (Cocreator)..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Paint" -Name "DisableCocreator" -Value 1
Set-RegDword -Path "HKCU:\Software\Microsoft\Paint" -Name "EnableCocreator" -Value 0

Write-Output "  [8/22] 禁用/移除 AI Fabric Service 服务..."
@("AIFabric", "AIFabricService", "CoreAIService") | ForEach-Object {
    Stop-Service -Name $_ -Force -ErrorAction SilentlyContinue
    Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\$_" -Name "Start" -Value 4 -Type DWord -ErrorAction SilentlyContinue
}
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsAI" -Name "DisableAIFabricService" -Value 1

Write-Output "  [9/22] 关闭 Windows AI Actions 联动功能..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsAI" -Name "DisableAiActions" -Value 1
Set-RegDword -Path "HKCU:\Software\Policies\Microsoft\Windows\WindowsAI" -Name "DisableAiActions" -Value 1

Write-Output "  [10/22] 关闭画图工具中的生成式填充与全部 AI 插件..."
Set-RegDword -Path "HKCU:\Software\Microsoft\Paint" -Name "DisableGenerativeFill" -Value 1
Set-RegDword -Path "HKCU:\Software\Microsoft\Paint" -Name "DisableAIFeatures" -Value 1

Write-Output "  [11/22] 禁用语音访问 (Voice Access) 自动下载 AI 语音模型..."
Set-RegDword -Path "HKCU:\Software\Microsoft\Speech_OneCore\Preferences" -Name "ModelDownloadEnabled" -Value 0
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\Speech" -Name "AllowSpeechModelDownload" -Value 0

Write-Output "  [12/22] 关闭 AI 语音增强 (Voice Clarity) 与演播室效果..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsAI" -Name "DisableVoiceClarity" -Value 1

Write-Output "  [13/22] 在系统设置与 Windows 搜索中禁用联网 AI 建议..."
Set-RegDword -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Search" -Name "BingSearchEnabled" -Value 0
Set-RegDword -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Search" -Name "CanCortanaBeEnabled" -Value 0
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\Windows Search" -Name "AllowCloudSearch" -Value 0
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\Windows Search" -Name "ConnectedSearchUseWeb" -Value 0

Write-Output "  [14/22] 配置策略防止 Windows 重新静默安装 AI 组件..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\CloudContent" -Name "DisableWindowsConsumerFeatures" -Value 1
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\CloudContent" -Name "DisableConsumerAccountStateContent" -Value 1

Write-Output "  [15/22] 注入全局与用户层面的 Copilot 策略封锁..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsCopilot" -Name "TurnOffWindowsCopilot" -Value 1
Set-RegDword -Path "HKCU:\Software\Policies\Microsoft\Windows\WindowsCopilot" -Name "TurnOffWindowsCopilot" -Value 1

Write-Output "  [16/22] 卸载 Copilot 及 AI 相关的 AppX 应用程序包..."
Get-AppxPackage -AllUsers | Where-Object { $_.Name -match "Copilot|Recall|Windows\.Ai\.|GenerativeAI" } | Remove-AppxPackage -AllUsers -ErrorAction SilentlyContinue
Get-AppxProvisionedPackage -Online | Where-Object { $_.DisplayName -match "Copilot|Recall|Windows\.Ai\.|GenerativeAI" } | Remove-AppxProvisionedPackage -Online -ErrorAction SilentlyContinue | Out-Null

Write-Output "  [17/22] 调用 DISM 移除 Recall 可选功能..."
try {
    Disable-WindowsOptionalFeature -Online -FeatureName "Recall" -NoRestart -WarningAction SilentlyContinue -ErrorAction SilentlyContinue | Out-Null
} catch {}

Write-Output "  [18/22] 扫描并移除 CBS 中的 AI 组件包..."
try {
    Get-WindowsPackage -Online -ErrorAction SilentlyContinue | Where-Object { $_.PackageName -like "*Recall*" -or $_.PackageName -like "*Windows-AI*" } | Remove-WindowsPackage -Online -NoRestart -ErrorAction SilentlyContinue | Out-Null
} catch {}

Write-Output "  [19/22] 隐藏 Windows 设置界面中的 AI 组件与 Recall 页面..."
$ctrlPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Control Panel\SettingsPageVisibility"
if (!(Test-Path $ctrlPath)) { New-Item -Path $ctrlPath -Force -ErrorAction SilentlyContinue | Out-Null }
Set-ItemProperty -Path $ctrlPath -Name "SettingsPageVisibility" -Value "hide:ai-components;recall;copilot" -Type String -Force -ErrorAction SilentlyContinue

Write-Output "  [20/22] 删除 Recall 快照文件与本地 CoreAI 模型缓存..."
$aiFolders = @(
    "$env:LOCALAPPDATA\Microsoft\Windows\Recall",
    "$env:PROGRAMDATA\Microsoft\Windows\Recall",
    "$env:LOCALAPPDATA\CoreAI",
    "$env:PROGRAMDATA\Microsoft\CoreAI"
)
foreach ($f in $aiFolders) {
    if (Test-Path $f) { Remove-Item -Path $f -Recurse -Force -ErrorAction SilentlyContinue }
}

Write-Output "  [21/22] 在记事本中禁用 AI 重写与润色功能..."
Set-RegDword -Path "HKLM:\SOFTWARE\Policies\Microsoft\Notepad" -Name "DisableAIFeatures" -Value 1
Set-RegDword -Path "HKCU:\Software\Microsoft\Notepad" -Name "EnableCowriter" -Value 0

Write-Output "  [22/22] 禁用 WindowsAI 与 Recall 的全部计划任务..."
Get-ScheduledTask -TaskPath "\Microsoft\Windows\WindowsAI\*" -ErrorAction SilentlyContinue | Disable-ScheduledTask -ErrorAction SilentlyContinue | Out-Null
Get-ScheduledTask -TaskPath "\Microsoft\Windows\Recall\*" -ErrorAction SilentlyContinue | Disable-ScheduledTask -ErrorAction SilentlyContinue | Out-Null

Write-Output "  [√] RemoveWindowsAI 对应的 22 项 AI 功能已全部清除！"
`
		return header + body, "22 项 Windows AI 特性已彻底删除", nil

	case OptActionRestorePinyin:
		body := `
Write-Output ">>> 正在解除布局代换并恢复微软拼音输入法..."
Remove-ItemProperty -Path "HKCU:\Keyboard Layout\Substitutes" -Name "00000804" -ErrorAction SilentlyContinue
Remove-ItemProperty -Path "Registry::HKEY_USERS\.DEFAULT\Keyboard Layout\Substitutes" -Name "00000804" -ErrorAction SilentlyContinue

$lang = Get-WinUserLanguageList
$zh = $lang | Where-Object { $_.LanguageTag -eq "zh-Hans-CN" }
$pinyin = "0804:{81D4E9C9-1D3B-41BC-9E6C-4B40BF79E35E}{FCEEBE02-D534-4D08-896A-770F5681D1B5}"
if ($zh -and -not ($zh.InputMethodTips -contains $pinyin)) {
    $zh.InputMethodTips.Add($pinyin)
    Set-WinUserLanguageList $lang -Force
}
Enable-ScheduledTask -TaskPath "\Microsoft\Windows\LanguageComponentsInstaller\" -TaskName "Installation" -ErrorAction SilentlyContinue | Out-Null
Enable-ScheduledTask -TaskPath "\Microsoft\Windows\LanguageComponentsInstaller\" -TaskName "ReconcileLanguageResources" -ErrorAction SilentlyContinue | Out-Null
Set-RegDword -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\SettingSync\Groups\Language" -Name "Enabled" -Value 1
Write-Output "  [√] 微软拼音已恢复！建议注销或重启生效。"
`
		return header + body, "微软拼音输入法已恢复", nil

	case OptActionRestoreUpdate:
		body := `
Write-Output ">>> 正在恢复 Windows 系统更新服务、策略与计划任务..."
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\wuauserv" -Name "Start" -Value 3 -Type DWord -ErrorAction SilentlyContinue
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WaaSMedicSvc" -Name "Start" -Value 3 -Type DWord -ErrorAction SilentlyContinue
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\UsoSvc" -Name "Start" -Value 2 -Type DWord -ErrorAction SilentlyContinue
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\bits" -Name "Start" -Value 3 -Type DWord -ErrorAction SilentlyContinue
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\dosvc" -Name "Start" -Value 3 -Type DWord -ErrorAction SilentlyContinue
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate"

$tasks = @(
    "\Microsoft\Windows\UpdateOrchestrator\Schedule Scan",
    "\Microsoft\Windows\UpdateOrchestrator\Schedule Scan Static Task",
    "\Microsoft\Windows\UpdateOrchestrator\UpdateModelTask",
    "\Microsoft\Windows\WaaSMedic\PerformRemediation",
    "\Microsoft\Windows\WindowsUpdate\Scheduled Start"
)
foreach ($t in $tasks) {
    $taskPath = $t.Substring(0, $t.LastIndexOf('\') + 1)
    $taskName = $t.Substring($t.LastIndexOf('\') + 1)
    Enable-ScheduledTask -TaskPath $taskPath -TaskName $taskName -ErrorAction SilentlyContinue | Out-Null
}
Start-Service -Name wuauserv -ErrorAction SilentlyContinue
Write-Output "  [√] Windows 系统更新服务已重置为默认状态！"
`
		return header + body, "Windows 系统更新已恢复默认状态", nil

	case OptActionRestoreAI:
		body := `
Write-Output ">>> 正在全量恢复 22 项 Windows AI 核心策略与系统组件..."
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsCopilot"
Remove-RegItemSafe -Path "HKCU:\Software\Policies\Microsoft\Windows\WindowsCopilot"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsAI"
Remove-RegItemSafe -Path "HKCU:\Software\Policies\Microsoft\Windows\WindowsAI"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Edge" -Name "HubsSidebarEnabled"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Edge" -Name "ShowCopilotButton"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Paint" -Name "DisableCocreator"
Remove-RegItemSafe -Path "HKCU:\Software\Microsoft\Paint" -Name "DisableGenerativeFill"
Remove-RegItemSafe -Path "HKCU:\Software\Microsoft\Paint" -Name "DisableAIFeatures"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Notepad" -Name "DisableAIFeatures"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\InputPersonalization"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\Windows Search" -Name "AllowCloudSearch"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\Windows Search" -Name "ConnectedSearchUseWeb"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\CloudContent" -Name "DisableWindowsConsumerFeatures"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\CloudContent" -Name "DisableConsumerAccountStateContent"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Policies\Microsoft\Windows\Speech" -Name "AllowSpeechModelDownload"
Remove-RegItemSafe -Path "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Control Panel\SettingsPageVisibility" -Name "SettingsPageVisibility"

Set-RegDword -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" -Name "ShowCopilotButton" -Value 1
Set-RegDword -Path "HKCU:\Software\Microsoft\Input\Settings" -Name "InsightsEnabled" -Value 1
Set-RegDword -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Search" -Name "BingSearchEnabled" -Value 1
Set-RegDword -Path "HKCU:\Software\Microsoft\Paint" -Name "EnableCocreator" -Value 1
Set-RegDword -Path "HKCU:\Software\Microsoft\Notepad" -Name "EnableCowriter" -Value 1
@("AIFabric", "AIFabricService", "CoreAIService") | ForEach-Object {
    Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\$_" -Name "Start" -Value 3 -Type DWord -ErrorAction SilentlyContinue
}

try {
    Enable-WindowsOptionalFeature -Online -FeatureName "Recall" -NoRestart -WarningAction SilentlyContinue -ErrorAction SilentlyContinue | Out-Null
} catch {}

Get-ScheduledTask -TaskPath "\Microsoft\Windows\WindowsAI\*" -ErrorAction SilentlyContinue | Enable-ScheduledTask -ErrorAction SilentlyContinue | Out-Null
Get-ScheduledTask -TaskPath "\Microsoft\Windows\Recall\*" -ErrorAction SilentlyContinue | Enable-ScheduledTask -ErrorAction SilentlyContinue | Out-Null

Write-Output "  [√] 全部 22 项 AI 策略已解除屏蔽，Recall 功能已重新激活！"
Write-Output "  提示：如需 Copilot 应用，可在微软应用商店 (Microsoft Store) 搜索重新安装。"
`
		return header + body, "22 项 Windows AI 支持已恢复", nil

	default:
		return "", "", fmt.Errorf("未知的优化操作: %s", action)
	}
}

// OptimizeExecute 执行深度优化或还原操作（GUI / CLI 共用，不读取 stdin）。
func OptimizeExecute(action, preset string) (map[string]interface{}, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if !optimizerAdminCheck() {
		return nil, errors.New("深度优化与净化需要管理员权限，请以管理员身份运行本工具")
	}

	if action == OptActionOptimizeAll {
		p, err := normalizePinyinPreset(preset)
		if err != nil {
			return nil, err
		}
		var outParts []string
		steps := []struct {
			act  string
			name string
		}{
			{OptActionPurgePinyin, "1/4 微软拼音删除与免疫"},
			{OptActionBlockUpdate, "2/4 封杀 Windows 系统更新"},
			{OptActionRemoveAI, "3/4 彻底删除 22 项 Windows AI"},
		}
		for _, step := range steps {
			script, _, err := buildOptimizerScript(step.act, p)
			if err != nil {
				return nil, err
			}
			out, runErr := optimizerRunner(script)
			if out != "" {
				outParts = append(outParts, out)
			}
			if runErr != nil {
				return map[string]interface{}{
					"ok":     false,
					"output": strings.Join(outParts, "\n\n"),
					"msg":    fmt.Sprintf("%s 执行未完全成功: %v", step.name, runErr),
				}, nil
			}
		}
		deleted, errs, bytes, gain := optimizerDeepClean()
		cleanSummary := fmt.Sprintf(">>> [4/4] 深度清理 C 盘垃圾完成：删除 %d 项，统计释放 %s", deleted, formatBytes(bytes))
		if gain > 0 {
			cleanSummary += fmt.Sprintf("（磁盘实测增加 %s）", formatBytes(gain))
		}
		if errs > 0 {
			cleanSummary += fmt.Sprintf("，%d 项因占用跳过", errs)
		}
		outParts = append(outParts, cleanSummary)
		outParts = append(outParts, "[完成] 全套深度优化已全部执行完毕！建议注销或重启一次电脑以彻底生效。")
		return map[string]interface{}{
			"ok":     true,
			"output": strings.Join(outParts, "\n\n"),
			"msg":    "一键终极精简已完成，建议注销或重启生效",
		}, nil
	}

	script, successMsg, err := buildOptimizerScript(action, preset)
	if err != nil {
		return nil, err
	}
	out, runErr := optimizerRunner(script)
	if runErr != nil {
		return map[string]interface{}{
			"ok":     false,
			"output": out,
			"msg":    "操作执行未成功完成: " + runErr.Error(),
		}, nil
	}
	return map[string]interface{}{
		"ok":     true,
		"output": out,
		"msg":    successMsg,
	}, nil
}
