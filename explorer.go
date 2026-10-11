package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// OpenInExplorer 用资源管理器打开目标；文件则定位选中，目录则打开。
//
// 健壮性：
//  1. 先展开路径里的环境变量（%VAR%）并转为绝对路径，避免把
//     "%LOCALAPPDATA%\..." 字面量交给 explorer（explorer 解析失败不报错，
//     而是静默打开"文档"目录）。
//  2. 文件定位使用 SHOpenFolderAndSelectItems，避免 /select 的引号及逗号解析歧义。
//  3. 路径无效时直接返回错误，不再盲目调用 explorer。
func OpenInExplorer(path string) error {
	p, directory, err := explorerTarget(path)
	if err != nil {
		return err
	}
	if !directory {
		return revealInExplorer(p)
	}
	cmd := exec.Command(filepath.Join(systemRoot(), "explorer.exe"), p)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func explorerTarget(path string) (string, bool, error) {
	p := expandWindowsEnv(strings.TrimSpace(path))
	if p == "" {
		return "", false, fmt.Errorf("路径为空")
	}
	info, err := os.Lstat(p)
	if err != nil {
		// 目标可能是注册表自启项里的「完整命令行」而非纯路径
		// （形如 `"C:\Program Files\App\app.exe" --flag`）。
		// 尝试提取其中的可执行文件路径再定位。
		if exe := parseExecutableFromCommand(p); exe != "" {
			if e2, err2 := os.Lstat(exe); err2 == nil {
				info, err = e2, nil
				p = exe
			}
		}
	}
	if err != nil {
		return "", false, fmt.Errorf("路径不存在: %s", p)
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return "", false, err
	}
	return p, info.IsDir(), nil
}

var (
	parseDisplayName    = shell32.NewProc("SHParseDisplayName")
	openFolderAndSelect = shell32.NewProc("SHOpenFolderAndSelectItems")
	ole32               = syscall.NewLazyDLL("ole32.dll")
	coInitialize        = ole32.NewProc("CoInitializeEx")
	coUninitialize      = ole32.NewProc("CoUninitialize")
	coTaskMemFree       = ole32.NewProc("CoTaskMemFree")
)

// Use Shell item IDs instead of Explorer's command-line parser so spaces,
// commas and Unicode names are passed without quoting ambiguities.
func revealInExplorer(path string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitialize.Call(0, 2)            // COINIT_APARTMENTTHREADED
	if int32(hr) < 0 && uint32(hr) != 0x80010106 { // already initialized as MTA
		return fmt.Errorf("初始化资源管理器失败 (HRESULT=0x%08x)", uint32(hr))
	}
	if int32(hr) >= 0 {
		defer coUninitialize.Call()
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var pidl uintptr
	hr, _, _ = parseDisplayName.Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0)
	if int32(hr) < 0 {
		return fmt.Errorf("解析文件路径失败 (HRESULT=0x%08x): %s", uint32(hr), path)
	}
	defer coTaskMemFree.Call(pidl)
	// cidl=0 means open the parent folder and select this fully specified item.
	hr, _, _ = openFolderAndSelect.Call(pidl, 0, 0, 0)
	if int32(hr) < 0 {
		return fmt.Errorf("定位文件失败 (HRESULT=0x%08x): %s", uint32(hr), path)
	}
	return nil
}

// parseExecutableFromCommand 从一段命令行里提取可执行文件路径。
// 支持带引号的 `"C:\path with space\app.exe"` 与不带引号的 `C:\app.exe --x`。
// 无法判定时返回空串。
func parseExecutableFromCommand(cmd string) string {
	s := strings.TrimSpace(cmd)
	if s == "" {
		return ""
	}
	if s[0] == '"' {
		// 引号闭合：取第一对引号内的内容
		if end := strings.Index(s[1:], `"`); end >= 0 {
			return s[1 : 1+end]
		}
		return ""
	}
	// 不带引号：取第一个空白前的部分
	if i := strings.IndexAny(s, " \t"); i > 0 {
		s = s[:i]
	}
	if filepath.Ext(s) == "" {
		return ""
	}
	return s
}

// askInt 读取一个非负整数；解析失败时返回 def。
func askInt(prompt string, def int) int {
	fmt.Print(prompt)
	var s string
	_, _ = fmt.Scanln(&s)
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// askYesNo 读取 y/n；默认返回 def（空回车视为默认）。
func askYesNo(prompt string, def bool) bool {
	fmt.Print(prompt + " [y/N] ")
	var s string
	_, _ = fmt.Scanln(&s)
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return def
	}
	return s == "y" || s == "yes" || s == "是"
}

// uniqueStrings 去掉重复并保持顺序。
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
