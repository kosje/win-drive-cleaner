package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ---------- 字节格式化 ----------

func formatBytes(n int64) string {
	if n < 0 {
		return "0 B"
	}
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.2f TB", float64(n)/float64(TB))
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.2f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// Resolve maintenance tools from Windows itself, never from a user-controlled PATH.
func windowsTool(name string, args ...string) *exec.Cmd {
	dir := "System32"
	if runtime.GOARCH == "386" && os.Getenv("PROCESSOR_ARCHITEW6432") != "" {
		if info, err := os.Stat(filepath.Join(systemRoot(), "Sysnative")); err == nil && info.IsDir() {
			dir = "Sysnative"
		}
	}
	return exec.Command(filepath.Join(systemRoot(), dir, name), args...)
}

// ---------- 路径解析（基于环境变量，兼容 Win8/10/11） ----------

// systemDrive 返回系统盘根目录，如 "C:\"。
func systemDrive() string {
	sd := strings.TrimSpace(os.Getenv("SystemDrive"))
	if sd == "" {
		sd = "C:"
	}
	return filepath.Clean(sd + "\\")
}

// systemRoot 返回 Windows 目录，如 "C:\Windows"。
func systemRoot() string {
	sr := strings.TrimSpace(os.Getenv("SystemRoot"))
	if sr == "" {
		sr = systemDrive() + "Windows"
	}
	return filepath.Clean(sr)
}

// localAppData 返回 %LOCALAPPDATA%。
func localAppData() string {
	if v := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); v != "" {
		return v
	}
	up := userProfile()
	return filepath.Join(up, "AppData", "Local")
}

// userProfile 返回 %USERPROFILE%。
func userProfile() string {
	if v := strings.TrimSpace(os.Getenv("USERPROFILE")); v != "" {
		return v
	}
	dir, _ := os.UserHomeDir()
	return dir
}

func expandWindowsEnv(s string) string {
	s = os.ExpandEnv(s)
	var out strings.Builder
	for {
		start := strings.IndexByte(s, '%')
		if start < 0 {
			out.WriteString(s)
			break
		}
		end := strings.IndexByte(s[start+1:], '%')
		if end < 0 {
			out.WriteString(s)
			break
		}
		end += start + 1
		out.WriteString(s[:start])
		if value, ok := os.LookupEnv(s[start+1 : end]); ok {
			out.WriteString(value)
		} else {
			out.WriteString(s[start : end+1])
		}
		s = s[end+1:]
	}
	return out.String()
}

// Read an entire line without buffering ahead of the other CLI prompts.
func readInputLine() (string, error) {
	var line strings.Builder
	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if n > 0 {
			if b[0] == '\n' {
				return strings.TrimSuffix(line.String(), "\r"), nil
			}
			line.WriteByte(b[0])
		}
		if err != nil {
			if err == io.EOF && line.Len() > 0 {
				return line.String(), nil
			}
			return line.String(), err
		}
	}
}

// allUsersProfile 返回 %ALLUSERSPROFILE%（C:\ProgramData）。
func allUsersProfile() string {
	if v := strings.TrimSpace(os.Getenv("ALLUSERSPROFILE")); v != "" {
		return v
	}
	return systemDrive() + "ProgramData"
}

// userTemp 返回当前用户临时目录 %TEMP%。
func userTemp() string {
	if v := strings.TrimSpace(os.Getenv("TEMP")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("TMP")); v != "" {
		return v
	}
	return filepath.Join(localAppData(), "Temp")
}

// recycleBin 返回系统盘回收站 $Recycle.Bin。
func recycleBin() string {
	return filepath.Join(systemDrive(), "$Recycle.Bin")
}

// windowsOld 返回旧的 Windows.old 目录。
func windowsOld() string {
	return filepath.Join(systemDrive(), "Windows.old")
}

// ---------- 系统信息 ----------

func osDescription() string {
	u := runtime.GOOS + "/" + runtime.GOARCH
	wd := systemRoot()
	return fmt.Sprintf("系统盘=%s  WINDIR=%s  架构=%s", systemDrive(), wd, u)
}

// nowStamp 返回当前时间戳字符串，用于日志头。
func nowStamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// ---------- 删除前备份日志（可追溯） ----------

// logDir 日志目录：%LOCALAPPDATA%\WinDriveCleaner
func logDir() string {
	return filepath.Join(localAppData(), "WinDriveCleaner")
}

// logDeletion 把一条"即将删除"记录追加写入当日日志文件。
// 失败静默（不影响清理主流程）。仅由实际删除分支调用。
func logDeletion(path string, bytes int64) {
	if err := os.MkdirAll(logDir(), 0755); err != nil {
		return
	}
	fname := "CleanerLog_" + time.Now().Format("20060102") + ".txt"
	f, err := os.OpenFile(filepath.Join(logDir(), fname), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "[%s] %s  (%s)\n", nowStamp(), path, formatBytes(bytes))
}
