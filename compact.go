package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
)

// compactGo 系统盘压缩（Windows 10/11 自带 LZX 压缩）。
// 把 C:\Windows 内文件就地 LZX 压缩存储，通常释放 1-3GB（机械硬盘收益最大）。
// 可逆：compact /CompactOS:never 可恢复。需管理员。
// 注意：只压缩系统文件，不影响用户数据；SSD 会轻微影响随机读寿命。

// compactSupported 检测 CompactOS 是否可用（Win10/11 支持）。
var compactSupported = sync.OnceValue(func() bool {
	cmd := windowsTool("compact.exe", "/?")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return err == nil && strings.Contains(strings.ToLower(string(out)), "/compactos")
})

// CompactExecute 执行系统盘压缩的**核心逻辑**，不依赖控制台输入，
// 因此可被 GUI（无控制台句柄）安全调用。返回 (结果, error)。
func CompactExecute(dryRun bool) (interface{}, error) {
	if !compactSupported() {
		return nil, errors.New("当前环境未检测到 CompactOS 系统压缩支持，已跳过")
	}
	if dryRun {
		return "演练模式，未执行任何操作", nil
	}
	if !isAdmin() {
		return nil, errors.New("系统盘压缩需要管理员权限，请以管理员身份运行本工具")
	}
	cmd := windowsTool("compact.exe", "/CompactOS:always")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = nil // 无控制台时显式置空，避免 Wait 卡在读 stdin
	out, err := cmd.CombinedOutput()
	tail := outputTail(string(out), 6)
	if err != nil {
		return map[string]interface{}{
			"ok": false, "output": tail, "msg": "compact 执行未成功完成: " + err.Error(),
		}, nil
	}
	return map[string]interface{}{
		"ok":     true,
		"output": tail,
		"msg":    "系统盘压缩完成",
	}, nil
}

// RunCompact 交互式执行系统盘压缩（CLI 版，带确认提示）。dryRun 时只打印将做什么。
func RunCompact(dryRun bool) {
	fmt.Println()
	fmt.Println("  ┌─ 系统盘压缩（可选进阶）─")
	fmt.Println("  │ 命令: compact /CompactOS:always")
	fmt.Println("  │ 效果: 把 C:\\Windows 内文件就地 LZX 压缩，通常释放 1-3GB（机械硬盘收益最大）。")
	fmt.Println("  │ 可逆: 日后运行 compact /CompactOS:never 可恢复原状。")

	if !compactSupported() {
		fmt.Println("  └ [不支持] 当前环境未检测到 CompactOS 系统压缩支持，跳过。")
		return
	}

	if dryRun {
		fmt.Println("  └ [演练] 未真正执行。")
		return
	}

	if !askYesNo("  确定压缩系统盘（可能需数分钟，期间勿强制断电）?", false) {
		fmt.Println("  已取消系统盘压缩。")
		return
	}

	fmt.Println("  正在压缩系统文件，请稍候（首次约 1-5 分钟）…")
	res, err := CompactExecute(false)
	if err != nil {
		fmt.Println("  ", err)
		return
	}
	m, _ := res.(map[string]interface{})
	if m == nil {
		fmt.Println("  ", res)
		return
	}
	if out, _ := m["output"].(string); out != "" {
		fmt.Println(out)
	}
	if msg, _ := m["msg"].(string); msg != "" {
		fmt.Println("  " + msg)
	}
}

// CompactFlowReadOnly 只读（--scan-compact 用）：只检测是否支持，不执行。
func CompactFlowReadOnly() {
	if compactSupported() {
		fmt.Println("  本系统支持系统压缩（compact /CompactOS:always）。在交互菜单 11) 可执行。")
	} else {
		fmt.Println("  当前环境未检测到 CompactOS 系统压缩支持。")
	}
}
