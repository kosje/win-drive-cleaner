package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// OpenInExplorer 用资源管理器打开目标；文件则定位选中，目录则打开。
//
// 健壮性：
//  1. 先展开路径里的环境变量（%VAR%）并转为绝对路径，避免把
//     "%LOCALAPPDATA%\..." 字面量交给 explorer（explorer 解析失败不报错，
//     而是静默打开"文档"目录）。
//  2. /select 参数必须写成 "/select,<path>"（逗号紧贴路径）——原实现写的是
//     "/select <path>"（缺逗号），explorer 无法识别，忽略后打开默认"文档"，
//     这正是"打开按钮都弹文档"的根因。
//  3. 路径无效时直接返回错误，不再盲目调用 explorer。
func OpenInExplorer(path string) error {
	p := os.ExpandEnv(strings.TrimSpace(path))
	if p == "" {
		return fmt.Errorf("路径为空")
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
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
		return fmt.Errorf("路径不存在: %s", p)
	}
	if !info.IsDir() {
		// 文件：选中定位，逗号紧贴路径
		return exec.Command("explorer.exe", "/select,"+p).Start()
	}
	return exec.Command("explorer.exe", p).Start()
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

// askInt 读取一个正整数；解析失败或 0 时返回 def。
func askInt(prompt string, def int) int {
	fmt.Print(prompt)
	var s string
	_, _ = fmt.Scanln(&s)
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
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
