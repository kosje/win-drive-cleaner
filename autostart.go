package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 自启动 + 倒计时自动执行。
// - 注册表 HKCU\...\Run 实现“登录自启”（纯系统 reg.exe，Win8–11 通用，零第三方依赖）
// - 设置保存在 %LOCALAPPDATA%\WinDriveCleaner\settings.json（倒计时秒数 + 上次清理模式/天数）
// - --auto 由 Run 键在登录时调用：倒计时后按“上次设置”自动清理，可按键取消

const (
	regRunKey    = `HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = `WinDriveCleaner`
)

// Settings 持久化的自启动/自动执行设置。
type Settings struct {
	Enabled      bool `json:"enabled"`      // 是否已注册登录自启
	CountdownSec int  `json:"countdownSec"` // 自动执行前的倒计时秒数
	Deep         bool `json:"deep"`         // 上次使用的模式（true=深度，false=普通）
	CommsDays    int  `json:"commsDays"`    // 上次通讯清理的天数
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
	if s.CountdownSec < 5 {
		s.CountdownSec = 30
	}
	if s.CommsDays < 0 {
		s.CommsDays = 0
	}
	return s
}

// saveSettings 持久化设置（自动建目录）。
func saveSettings(s Settings) {
	p := settingsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(p, b, 0644)
}

// regRun 调用系统自带 reg.exe。
func regRun(args ...string) (string, error) {
	out, err := exec.Command("reg", args...).CombinedOutput()
	return string(out), err
}

// registerRunKey 开/关 登录自启（HKCU Run 键）。
func registerRunKey(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位自身路径")
	}
	if on {
		data := `"` + exe + `" --auto`
		_, err := regRun("add", regRunKey, "/v", runValueName, "/t", "REG_SZ", "/d", data, "/f")
		return err
	}
	out, err := regRun("delete", regRunKey, "/v", runValueName, "/f")
	if err != nil {
		// 值本就不存在则视为成功
		if strings.Contains(strings.ToLower(out), "not found") ||
			strings.Contains(out, "找不到") {
			return nil
		}
		return err
	}
	return nil
}

// autostartRegistered 查询当前是否已注册登录自启。
func autostartRegistered() bool {
	out, err := regRun("query", regRunKey, "/v", runValueName)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(out), strings.ToLower(runValueName))
}

// describeSettings 返回设置的中文描述。
func describeSettings(s Settings) string {
	mode := "普通清理"
	if s.Deep {
		mode = "深度清理"
	}
	return fmt.Sprintf("%s | 通讯清理 %d 天前 | 倒计时 %d 秒 | 登录自启=%s",
		mode, s.CommsDays, s.CountdownSec, onOff(s.Enabled || autostartRegistered()))
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

	DryRun = false
	if !execute(s.Deep, noUAC) { // 提权子进程接管，父进程不重复清理通讯文件
		return
	}
	if lastCommsDays > 0 {
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
		fmt.Printf("   %-18s 已删除 %d 个文件，释放 %s%s\n", t.Name, n, formatBytes(b), errNote(int(e)))
	}
}

// countdownCancel 倒计时；期间按任意键则立即取消。返回 true=用户取消。
func countdownCancel(secs int) bool {
	if secs < 5 {
		secs = 30
	}
	fmt.Printf("\n  将在 %d 秒后自动执行，按任意键可取消…\n", secs)
	cancelled := false
	pressCh := make(chan struct{}, 1)
	go func() {
		var d string
		_, _ = fmt.Scanln(&d)
		select {
		case pressCh <- struct{}{}:
		default:
		}
	}()
	for i := secs; i > 0; i-- {
		select {
		case <-pressCh:
			cancelled = true
		default:
		}
		if cancelled {
			break
		}
		fmt.Printf("\r  剩余 %2d 秒，按任意键取消…   ", i)
		time.Sleep(1 * time.Second)
	}
	fmt.Println()
	return cancelled
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
		saveSettings(s)
	case "b":
		if err := registerRunKey(false); err != nil {
			fmt.Println("  关闭失败:", err)
		} else {
			s.Enabled = false
			fmt.Println("  已关闭登录自启动，下次启动不再自动执行。")
		}
		saveSettings(s)
	case "c":
		s.CountdownSec = askInt("  倒计时秒数(默认 30): ", s.CountdownSec)
		saveSettings(s)
		fmt.Printf("  倒计时已设为 %d 秒。\n", s.CountdownSec)
	case "d":
		s.Deep = true
		saveSettings(s)
		fmt.Println("  自动执行模式已设为“深度清理”。")
	case "e":
		s.Deep = false
		saveSettings(s)
		fmt.Println("  自动执行模式已设为“普通清理”。")
	}
}
