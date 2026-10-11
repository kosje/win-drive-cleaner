package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 开机自启动管理（纯逻辑，不打印，供 CLI 与 GUI 复用）。
//
// 覆盖两类来源：
//  1) 注册表 Run / RunOnce（HKCU + HKLM 各一对）：
//     采用 Windows 任务管理器原生的 StartupApproved 机制做“可逆禁用”。
//     - 启用：删除对应 StartupApproved\Run|\RunOnce 下的值（值不存在即视为启用）
//     - 禁用：写入 16 字节、首字节 0x03 的二进制值（与任务管理器禁用一致，完全可逆）
//  2) 启动文件夹 .lnk 快捷方式（当前用户 + ProgramData）：
//     采用“改名加后缀”的可逆禁用法，恢复即改回原名。
//
// 设计原则：所有操作可逆、不改名注册表值本身、不删除程序文件。

// StartupSource 描述一个自启动条目来自何处。
type StartupSource int

const (
	// RegRunCU：HKCU\...\CurrentVersion\Run
	RegRunCU StartupSource = iota
	// RegRunOnceCU：HKCU\...\CurrentVersion\RunOnce
	RegRunOnceCU
	// RegRunLM：HKLM\...\CurrentVersion\Run
	RegRunLM
	// RegRunOnceLM：HKLM\...\CurrentVersion\RunOnce
	RegRunOnceLM
	// StartupFolderUser：当前用户 开始菜单\启动
	StartupFolderUser
	// StartupFolderCommon：ProgramData 公共启动
	StartupFolderCommon
)

func (s StartupSource) String() string {
	switch s {
	case RegRunCU:
		return "当前用户 · Run"
	case RegRunOnceCU:
		return "当前用户 · RunOnce"
	case RegRunLM:
		return "所有用户 · Run"
	case RegRunOnceLM:
		return "所有用户 · RunOnce"
	case StartupFolderUser:
		return "启动文件夹(当前用户)"
	case StartupFolderCommon:
		return "启动文件夹(所有用户)"
	default:
		return "未知"
	}
}

// isRegistry 该来源是否为注册表项。
func (s StartupSource) isRegistry() bool {
	return s >= RegRunCU && s <= RegRunOnceLM
}

// isLMHive 该来源是否属于 HKLM（写它需要管理员）。
func (s StartupSource) isLMHive() bool {
	return s == RegRunLM || s == RegRunOnceLM
}

func (s StartupSource) needsAdmin() bool {
	return s.isLMHive() || s == StartupFolderCommon
}

func (s StartupSource) isRunOnce() bool {
	return s == RegRunOnceCU || s == RegRunOnceLM
}

// startupDisabledSuffix 启动文件夹被禁用时追加的后缀。
const startupDisabledSuffix = ".wcd_disabled"

// StartupEntry 一条开机自启动项。
type StartupEntry struct {
	// ID 稳定标识（注册表项包含 hive、键路径、值名；启动文件夹为全路径）。
	ID string
	// Name 展示名称。
	Name string
	// Target 注册表项=完整命令行；启动文件夹=快捷方式全路径。
	Target string
	// Source 来源。
	Source StartupSource
	// Enabled 当前是否启用。
	Enabled bool
}

// StartupResult 一次禁用/恢复操作的结果。
type StartupResult struct {
	OK    bool
	Msg   string
	Error string
}

// regRunLoc 一个注册表 Run/RunOnce 来源的元数据。
type regRunLoc struct {
	source   StartupSource
	hive     registry.Key
	regPath  string // 值所在的 Run/RunOnce 键
	approved string // StartupApproved 子键路径
}

// regRunLocations 注册表运行项的四个来源。
var regRunLocations = []regRunLoc{
	{RegRunCU, registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`},
	{RegRunOnceCU, registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\RunOnce`,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\RunOnce`},
	{RegRunLM, registry.LOCAL_MACHINE,
		`Software\Microsoft\Windows\CurrentVersion\Run`,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`},
	{RegRunOnceLM, registry.LOCAL_MACHINE,
		`Software\Microsoft\Windows\CurrentVersion\RunOnce`,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\RunOnce`},
}

// hiveName 返回可读的 hive 前缀。
func (loc regRunLoc) hiveName() string {
	if loc.hive == registry.LOCAL_MACHINE {
		return "HKLM"
	}
	return "HKCU"
}

// listRegStartup 枚举某注册表 Run/RunOnce 键下的所有值。
func listRegStartup(loc regRunLoc) []StartupEntry {
	var out []StartupEntry
	k, err := registry.OpenKey(loc.hive, loc.regPath, registry.READ)
	if err != nil {
		return out // 键不存在（如某机器没有 RunOnce）→ 空
	}
	defer k.Close()

	names, err := k.ReadValueNames(0)
	if err != nil {
		return out
	}
	for _, name := range names {
		str, _, gErr := k.GetStringValue(name)
		val := ""
		if gErr == nil {
			val = str
		}
		out = append(out, StartupEntry{
			ID:      loc.hiveName() + ":" + loc.regPath + ":" + name,
			Name:    name,
			Target:  val,
			Source:  loc.source,
			Enabled: isRegEnabled(loc, name),
		})
	}
	return out
}

// isRegEnabled 判定注册表某运行项当前是否启用（读 StartupApproved）。
// 规则（与任务管理器一致，已核实）：
//   - StartupApproved\Run|\RunOnce 下无该值 → 视为启用
//   - 值存在且首字节 0x02 → 启用；其余（如 0x03）→ 禁用
func isRegEnabled(loc regRunLoc, valueName string) bool {
	if loc.source.isRunOnce() {
		// StartupApproved does not control one-time RunOnce execution.
		return true
	}
	ak, err := registry.OpenKey(loc.hive, loc.approved, registry.READ)
	if err != nil {
		// StartupApproved\Run 键本身可能不存在 → 视为启用
		return true
	}
	defer ak.Close()
	blob, _, gErr := ak.GetBinaryValue(valueName)
	if gErr != nil {
		// 该值不存在 → 视为启用（系统原生行为）
		return true
	}
	if len(blob) == 0 {
		return true
	}
	return blob[0] == 0x02
}

// setRegEnabled 开/关注册表某运行项。on=true 启用（删除 StartupApproved 值），
// on=false 禁用（写 16 字节、首字节 0x03 的 StartupApproved 值）。
func setRegEnabled(loc regRunLoc, valueName string, on bool) StartupResult {
	if loc.source.isRunOnce() {
		return StartupResult{OK: false, Error: "RunOnce 一次性启动项不支持 StartupApproved 开关，当前仅供查看"}
	}
	if on {
		ak, err := registry.OpenKey(loc.hive, loc.approved, registry.SET_VALUE|registry.READ)
		if err != nil {
			if isKeyNotValue(err) {
				return StartupResult{OK: true, Msg: "已启用（该启动项本未禁用）"}
			}
			return StartupResult{OK: false, Error: fmt.Sprintf("无法启用: %v", err)}
		}
		defer ak.Close()
		if err := ak.DeleteValue(valueName); err != nil && !isKeyNotValue(err) {
			return StartupResult{OK: false, Error: fmt.Sprintf("启用失败: %v", err)}
		}
		return StartupResult{OK: true, Msg: "已启用"}
	}

	// 禁用：确保 StartupApproved 子键存在，再写 16 字节 0x03 标记
	ak, _, err := registry.CreateKey(loc.hive, loc.approved, registry.SET_VALUE|registry.READ|registry.CREATE_SUB_KEY)
	if err != nil {
		return StartupResult{OK: false, Error: fmt.Sprintf("无法创建禁用标记键: %v", err)}
	}
	defer ak.Close()
	blob := make([]byte, 16)
	blob[0] = 0x03
	if err := ak.SetBinaryValue(valueName, blob); err != nil {
		return StartupResult{OK: false, Error: fmt.Sprintf("写入禁用标记失败: %v", err)}
	}
	return StartupResult{OK: true, Msg: "已禁用（可恢复）"}
}

// isKeyNotValue 判定注册表错误是否为“值不存在”（ERROR_FILE_NOT_FOUND）。
func isKeyNotValue(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// ---------- 启动文件夹 ----------

// startupFolders 启动文件夹：当前用户 + 所有用户（ProgramData）。
func startupFolders() map[StartupSource]string {
	um := map[StartupSource]string{}
	// 当前用户
	um[StartupFolderUser] = filepath.Join(appDataDir(), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if um[StartupFolderUser] == "Microsoft\\Windows\\Start Menu\\Programs\\Startup" {
		um[StartupFolderUser] = ""
	}
	// 所有用户
	um[StartupFolderCommon] = filepath.Join(allUsersProfile(), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if um[StartupFolderCommon] == "Microsoft\\Windows\\Start Menu\\Programs\\Startup" {
		um[StartupFolderCommon] = ""
	}
	return um
}

// listStartupFolder 枚举一个启动文件夹里的 .lnk/.url 快捷方式。
func listStartupFolder(src StartupSource, dir string) []StartupEntry {
	var out []StartupEntry
	if dir == "" {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fn := e.Name()
		disabled := strings.HasSuffix(strings.ToLower(fn), startupDisabledSuffix)
		display := filepath.Base(startupFolderPath(fn, true))
		ext := strings.ToLower(filepath.Ext(display))
		if ext != ".lnk" && ext != ".url" && ext != ".cmd" && ext != ".bat" && ext != ".com" && ext != ".exe" {
			continue
		}
		full := filepath.Join(dir, fn)
		out = append(out, StartupEntry{
			ID:      full,
			Name:    display,
			Target:  full,
			Source:  src,
			Enabled: !disabled,
		})
	}
	return out
}

// setStartupFolderEnabled 开/关一个启动文件夹里的快捷方式（改名加/去后缀，可逆）。
func setStartupFolderEnabled(src StartupSource, full string, on bool) StartupResult {
	if (src != StartupFolderUser && src != StartupFolderCommon) || hasReparseAncestor(full) {
		return StartupResult{Error: "启动文件路径无效或包含目录联接"}
	}
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return StartupResult{Error: "启动文件不存在或不是普通文件"}
	}
	target := startupFolderPath(full, on)
	if !samePath(full, target) {
		from, err := windows.UTF16PtrFromString(full)
		if err != nil {
			return StartupResult{Error: err.Error()}
		}
		to, err := windows.UTF16PtrFromString(target)
		if err != nil {
			return StartupResult{Error: err.Error()}
		}
		// No REPLACE_EXISTING: never overwrite a shortcut installed meanwhile.
		if err := windows.MoveFileEx(from, to, 0); err != nil {
			return StartupResult{Error: fmt.Sprintf("修改启动文件失败（同名目标不会覆盖）: %v", err)}
		}
	}
	msg := "已禁用（可恢复）"
	if on {
		msg = "已启用"
	}
	return StartupResult{OK: true, Msg: msg}
}

func startupFolderPath(full string, on bool) string {
	disabled := strings.HasSuffix(strings.ToLower(full), startupDisabledSuffix)
	if on && disabled {
		return full[:len(full)-len(startupDisabledSuffix)]
	}
	if !on && !disabled {
		return full + startupDisabledSuffix
	}
	return full
}

// ---------- 对外统一接口（供 CLI/GUI 调用） ----------

// enumerateStartups 返回全部开机自启动条目（注册表 4 组 + 启动文件夹 2 组）。
func enumerateStartups() []StartupEntry {
	var all []StartupEntry
	for _, loc := range regRunLocations {
		all = append(all, listRegStartup(loc)...)
	}
	folders := startupFolders()
	all = append(all, listStartupFolder(StartupFolderUser, folders[StartupFolderUser])...)
	all = append(all, listStartupFolder(StartupFolderCommon, folders[StartupFolderCommon])...)
	return all
}

// resolveRegLoc 根据条目来源找到对应 regRunLoc。
func resolveRegLoc(src StartupSource) (regRunLoc, bool) {
	for _, loc := range regRunLocations {
		if loc.source == src {
			return loc, true
		}
	}
	return regRunLoc{}, false
}

// setValueEnabled 对单个条目执行 启用/禁用（on=true 启用，false 禁用）。
func setValueEnabled(e *StartupEntry, on bool) StartupResult {
	if e.Source.needsAdmin() && !isAdmin() {
		return StartupResult{OK: false, Error: "修改所有用户启动项需要管理员权限"}
	}
	switch {
	case e.Source.isRegistry():
		loc, _ := resolveRegLoc(e.Source)
		return setRegEnabled(loc, e.Name, on)
	case e.Source == StartupFolderUser || e.Source == StartupFolderCommon:
		res := setStartupFolderEnabled(e.Source, e.ID, on)
		if res.OK {
			e.ID = startupFolderPath(e.ID, on)
			e.Target, e.Enabled = e.ID, on
		}
		return res
	default:
		return StartupResult{OK: false, Error: "不支持的来源类型"}
	}
}
