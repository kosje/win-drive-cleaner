package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 自启动 + 倒计时自动执行。
// - 注册表 HKCU\...\Run 实现登录自启，StartupApproved 控制启用状态
// - 设置保存在 %LOCALAPPDATA%\WinDriveCleaner\settings.json（倒计时秒数 + 上次清理模式/天数）
// - --auto 由 Run 键在登录时调用：倒计时后按“上次设置”自动清理，可按键取消

const (
	runValueName = `WinDriveCleaner`
)

// Settings 持久化的自启动/自动执行设置。
type Settings struct {
	Enabled      bool `json:"enabled"`      // 是否已注册登录自启
	CountdownSec int  `json:"countdownSec"` // 自动执行前的倒计时秒数
	Deep         bool `json:"deep"`         // 上次使用的模式（true=深度，false=普通）
	CommsDays    int  `json:"commsDays"`    // 上次通讯清理的天数
	CleanComms   bool `json:"cleanComms"`   // 自动通讯清理必须单独开启
}

// lastCommsDays 当前运行使用的通讯清理天数（自动模式下取“上次设置”）。
var lastCommsDays int

// settingsPath 返回设置文件路径。
func settingsPath() string {
	return filepath.Join(localAppData(), "WinDriveCleaner", "settings.json")
}

// defaultSettings 返回默认设置。
func defaultSettings() Settings {
	return Settings{CountdownSec: 30, CommsDays: 30}
}

// loadSettings 读取设置；不存在/损坏时返回默认。
func loadSettings() Settings {
	s := defaultSettings()
	b, err := os.ReadFile(settingsPath())
	if err != nil {
		return s
	}
	if e := json.Unmarshal(b, &s); e != nil {
		return defaultSettings()
	}
	if s.CountdownSec < 5 || s.CountdownSec > 3600 {
		s.CountdownSec = 30
	}
	if s.CommsDays < 0 || s.CommsDays > 36500 {
		s.CommsDays = 0
	}
	return s
}

// saveSettings 持久化设置（自动建目录）。
func saveSettings(s Settings) error {
	if s.CountdownSec < 5 || s.CountdownSec > 3600 {
		return fmt.Errorf("倒计时必须为 5–3600 秒")
	}
	if s.CommsDays < 0 || s.CommsDays > 36500 {
		return fmt.Errorf("通讯清理天数必须为 0–36500")
	}
	p := settingsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".settings-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(f.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func persistSettings(s Settings) bool {
	if err := saveSettings(s); err != nil {
		fmt.Printf("  设置保存失败，原设置已保留: %v\n", err)
		return false
	}
	return true
}

// registerRunKey 开/关 登录自启（HKCU Run 键）。
func registerRunKey(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位自身路径")
	}
	return registerSelfStartup(regRunLocations[0], on, quoteArgs([]string{exe, "--auto"}))
}

func registerSelfStartup(loc regRunLoc, on bool, command string) error {
	if !on {
		key, err := registry.OpenKey(loc.hive, loc.regPath, registry.SET_VALUE)
		if isKeyNotValue(err) {
			return nil
		}
		if err != nil {
			return err
		}
		defer key.Close()
		err = key.DeleteValue(runValueName)
		if isKeyNotValue(err) {
			return nil
		}
		return err
	}
	key, _, err := registry.CreateKey(loc.hive, loc.regPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	previous, kind, readErr := key.GetStringValue(runValueName)
	if readErr != nil && !isKeyNotValue(readErr) {
		return readErr
	}
	if err := key.SetStringValue(runValueName, command); err != nil {
		return err
	}
	result := setRegEnabled(loc, runValueName, true)
	if !result.OK {
		var rollbackErr error
		if isKeyNotValue(readErr) {
			rollbackErr = key.DeleteValue(runValueName)
		} else if kind == registry.EXPAND_SZ {
			rollbackErr = key.SetExpandStringValue(runValueName, previous)
		} else {
			rollbackErr = key.SetStringValue(runValueName, previous)
		}
		if rollbackErr != nil {
			return fmt.Errorf("无法恢复登录自启: %s；还原注册值失败: %w", result.Error, rollbackErr)
		}
		return fmt.Errorf("无法恢复登录自启: %s", result.Error)
	}
	return nil
}

// autostartRegistered 查询当前是否已注册登录自启。
func autostartRegistered() bool {
	return selfStartupRegistered(regRunLocations[0])
}

func selfStartupRegistered(loc regRunLoc) bool {
	key, err := registry.OpenKey(loc.hive, loc.regPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	command, _, err := key.GetStringValue(runValueName)
	return err == nil && strings.TrimSpace(command) != "" && isRegEnabled(loc, runValueName)
}

// describeSettings 返回设置的中文描述。
func describeSettings(s Settings) string {
	mode := "普通清理"
	if s.Deep {
		mode = "深度清理"
	}
	comms := "关闭"
	if s.CleanComms && s.CommsDays > 0 {
		comms = fmt.Sprintf("%d 天前", s.CommsDays)
	}
	return fmt.Sprintf("%s | 自动通讯清理=%s | 倒计时 %d 秒 | 登录自启=%s",
		mode, comms, s.CountdownSec, onOff(autostartRegistered()))
}

func onOff(b bool) string {
	if b {
		return "已开启"
	}
	return "关闭"
}

// runAutoFlow 自动模式：倒计时（可取消）→ 按上次设置执行。
// 登录自启进程通常非管理员；深度清理时由 execute 自动 UAC 提权重启
// （elevateSelf 保留 --auto --elevated，子进程接管时跳过倒计时直接执行）。
func runAutoFlow() {
	s := loadSettings()
	lastCommsDays = s.CommsDays
	noPause = true // 自动后台执行：不暂停，避免登录时被卡住

	if !elevated {
		if countdownCancel(s.CountdownSec) {
			fmt.Println("  已取消本次自动执行。")
			fmt.Println("  如需关闭自启动，请在交互菜单选择“8) 自启动管理”。")
			return
		}
	}

	if !execute(s.Deep, noUAC) { // 提权子进程接管，父进程不重复清理通讯文件
		return
	}
	if s.CleanComms && lastCommsDays > 0 {
		commsAutoDelete(lastCommsDays)
	}
}

// commsAutoDelete 非交互删除所有通讯软件 days 天前的文件（用于自动模式）。
func commsAutoDelete(days int) {
	for _, t := range commsTargets() {
		b, n, e := deleteCommsOlder(t, days)
		if b == 0 && n == 0 {
			continue
		}
		verb := "已删除"
		if DryRun {
			verb = "预计清理"
		}
		fmt.Printf("   %-18s %s %d 个文件，大小 %s%s\n", t.Name, verb, n, formatBytes(b), errNote(int(e)))
	}
}

// countdownCancel 倒计时；期间按回车取消。返回 true=用户取消。
func countdownCancel(secs int) bool {
	if secs < 5 {
		secs = 30
	}
	fmt.Printf("\n  将在 %d 秒后自动执行，按回车可取消…\n", secs)
	pressCh := make(chan struct{}, 1)
	go func() {
		var d string
		_, _ = fmt.Scanln(&d)
		select {
		case pressCh <- struct{}{}:
		default:
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	cancelled := waitCountdown(secs, pressCh, ticker.C)
	fmt.Println()
	return cancelled
}

func waitCountdown(secs int, cancel <-chan struct{}, ticks <-chan time.Time) bool {
	for i := secs; i > 0; i-- {
		fmt.Printf("\r  剩余 %2d 秒，按回车取消…   ", i)
		select {
		case <-cancel:
			return true
		case <-ticks:
		}
	}
	// A key received during the final interval must still cancel execution.
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

// manageAutostart 交互：管理自启动（开/关 + 设倒计时 + 看上次设置）。
func manageAutostart() {
	s := loadSettings()
	s.Enabled = autostartRegistered()
	fmt.Printf("\n  当前: %s\n", describeSettings(s))
	fmt.Println("  请选择:")
	fmt.Println("   a) 开启登录自启动")
	fmt.Println("   b) 关闭登录自启动")
	fmt.Println("   c) 设置自动执行前的倒计时秒数")
	fmt.Println("   d) 设为深度模式")
	fmt.Println("   e) 设为普通模式")
	fmt.Println("   f) 设置自动通讯旧文件清理（默认关闭）")
	fmt.Println("   q) 返回")
	var c string
	_, _ = fmt.Scanln(&c)
	c = strings.ToLower(strings.TrimSpace(c))
	switch c {
	case "a":
		if err := registerRunKey(true); err != nil {
			fmt.Println("  开启失败:", err)
		} else {
			s.Enabled = true
			fmt.Println("  已开启登录自启动：每次登录后将倒计时自动执行。")
		}
		if !persistSettings(s) {
			return
		}
	case "b":
		if err := registerRunKey(false); err != nil {
			fmt.Println("  关闭失败:", err)
		} else {
			s.Enabled = false
			fmt.Println("  已关闭登录自启动，下次启动不再自动执行。")
		}
		if !persistSettings(s) {
			return
		}
	case "c":
		s.CountdownSec = askInt("  倒计时秒数(默认 30): ", s.CountdownSec)
		if !persistSettings(s) {
			return
		}
		fmt.Printf("  倒计时已设为 %d 秒。\n", s.CountdownSec)
	case "d":
		s.Deep = true
		if !persistSettings(s) {
			return
		}
		fmt.Println("  自动执行模式已设为“深度清理”。")
	case "e":
		s.Deep = false
		if !persistSettings(s) {
			return
		}
		fmt.Println("  自动执行模式已设为“普通清理”。")
	case "f":
		s.CommsDays = askInt("  自动删除多少天前的通讯文件？(0=关闭): ", 0)
		s.CleanComms = s.CommsDays > 0
		if s.CleanComms && !askYesNo("  自动删除旧附件不可恢复，确定启用自动通讯清理？", false) {
			s.CleanComms = false
		}
		if !persistSettings(s) {
			return
		}
		fmt.Printf("  自动通讯清理: %s\n", onOff(s.CleanComms))
	}
}
