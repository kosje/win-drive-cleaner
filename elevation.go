package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// 通过纯标准库 syscall 调用 shell32，零外部依赖，Win8/10/11 通用。
var (
	shell32     = syscall.NewLazyDLL("shell32.dll")
	shellExecW  = shell32.NewProc("ShellExecuteW")
	isUserAdmin = shell32.NewProc("IsUserAnAdmin")
)

// isAdmin 判断当前进程是否以管理员权限运行。
func isAdmin() bool {
	r, _, _ := isUserAdmin.Call()
	return r != 0
}

// elevateSelf 以管理员身份重新启动自身，携带原有参数并追加 --elevated。
func elevateSelf() error {
	return elevateSelfWithArgs()
}

// elevateSelfWithArgs 以管理员身份重新启动自身，在原有参数基础上追加 extraArgs。
//
// 用于 GUI 场景：普通模式下启动的 GUI 进程 os.Args 里没有 "--gui"，
// 直接复用 elevateSelf 会退回交互菜单，所以需显式补上 --gui。
func elevateSelfWithArgs(extraArgs ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位自身路径: %w", err)
	}

	args := elevationArgs(os.Args[1:], extraArgs)

	verb := syscall.StringToUTF16Ptr("runas")
	file := syscall.StringToUTF16Ptr(exe)
	paramStr := syscall.StringToUTF16Ptr(quoteArgs(args))
	dir := syscall.StringToUTF16Ptr("")

	ret, _, callErr := shellExecW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(paramStr)),
		uintptr(unsafe.Pointer(dir)),
		1, // SW_SHOWNORMAL
	)

	return shellExecuteError(ret, callErr)
}

func shellExecuteError(ret uintptr, callErr error) error {
	if ret > 32 {
		return nil
	}
	if callErr == syscall.Errno(1223) {
		return fmt.Errorf("你已取消 UAC 权限请求")
	}
	return fmt.Errorf("启动提权进程失败 (code=%d): %v", ret, callErr)
}

func elevationArgs(original, extra []string) []string {
	args := make([]string, 0, len(original)+len(extra)+1)
	for _, a := range original {
		name := strings.SplitN(strings.TrimLeft(a, "-"), "=", 2)[0]
		if name == "no-uac" || name == "elevated" {
			continue
		}
		if len(extra) > 0 {
			if name == "no-pause" {
				args = append(args, a)
			}
			continue
		}
		args = append(args, a)
	}
	args = append(args, extra...)
	return append(args, "--elevated")
}

// quoteArgs 把参数数组拼成命令行字符串，含空格的参数加引号。
func quoteArgs(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, syscall.EscapeArg(a))
	}
	return strings.Join(out, " ")
}
