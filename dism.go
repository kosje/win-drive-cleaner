package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// outputTail 取命令输出末尾 n 行非空文本，作为执行摘要反馈给用户
// （DISM / compact 的完整输出动辄上百行，全塞进 GUI 只会淹没界面）。
func outputTail(s string, n int) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		t := strings.TrimSpace(lines[i])
		if t != "" {
			out = append(out, t)
		}
	}
	// 反转回正序
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return strings.Join(out, "\n")
}

// DismExecute 执行 DISM 组件清理的**核心逻辑**，不依赖控制台输入。
//
// 与 RunDismCleanup 的区别：这里不做 y/n 确认、不读 stdin，
// 因此可以安全地被 GUI（无控制台句柄）调用。返回 (结果消息, error)。
// dryRun=true 时只说明将执行什么，不真正运行。
func DismExecute(dryRun bool) (interface{}, error) {
	if dryRun {
		return "演练模式，未执行任何操作", nil
	}
	if !isAdmin() {
		return nil, errors.New("DISM 需要管理员权限，请以管理员身份运行本工具")
	}
	cmd := windowsTool("dism.exe", "/Online", "/Cleanup-Image", "/StartComponentCleanup")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = nil // 无控制台时显式置空，避免 Wait 卡在读 stdin
	out, err := cmd.CombinedOutput()
	// DISM 的进度输出走 stderr 之外的通道也会合并进来，取尾部若干行做摘要
	tail := outputTail(string(out), 6)
	if err != nil {
		return map[string]interface{}{
			"ok":     false,
			"output": tail,
			"msg":    "DISM 执行未成功完成: " + err.Error(),
		}, nil
	}
	return map[string]interface{}{
		"ok":     true,
		"output": tail,
		"msg":    "DISM 组件清理完成",
	}, nil
}

// RunDismCleanup 运行 DISM 组件清理（CLI 版，带确认提示）。
func RunDismCleanup(dryRun bool) {
	fmt.Println()
	fmt.Println("  ┌─ DISM 组件清理（可选）─")
	fmt.Println("  │ 这会清理 WinSxS 组件备份，通常可再释放数百 MB 到数 GB，但耗时较长。")
	fmt.Println("  │ 命令: DISM /Online /Cleanup-Image /StartComponentCleanup")
	if dryRun {
		fmt.Println("  └ [演练] 未真正执行。")
		return
	}
	if !askYesNo("  确定执行 DISM 组件清理吗（可能需数分钟）?", false) {
		fmt.Println("  已取消 DISM 组件清理。")
		return
	}
	fmt.Println("  正在运行 DISM，请稍候…")
	res, err := DismExecute(false)
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

// PackLeftovers 把若干临时目录中"删不掉/仍残留"的小文件压缩成一个 zip，便于用户手动转移/删除。
// 典型用法：在深度清理删除后调用——此时目录里剩下的就是被占用或无权限的文件。
// 返回 (zip 路径, 打包条数, error)；无残留时路径为空。
func PackLeftovers() (string, int, error) {
	roots := []string{
		filepath.Join(systemRoot(), "Temp"),
		userTemp(),
		filepath.Join(allUsersProfile(), "Microsoft", "Windows", "WER"),
	}
	type candidate struct{ path, name string }
	var keep []candidate
	seen := map[string]bool{}
	labels := []string{"WindowsTemp", "UserTemp", "WER"}
	for i, root := range roots {
		safeWalk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || !info.Mode().IsRegular() {
				return nil
			}
			if len(keep) >= 5000 {
				return filepath.SkipAll
			}
			key := strings.ToLower(filepath.Clean(p))
			if seen[key] || (strings.HasPrefix(info.Name(), "CleanerPack_") && strings.EqualFold(filepath.Ext(p), ".zip")) {
				return nil
			}
			// 只打包 50MB 以下的单个文件，避免超大文件
			if info.Size() <= 50<<20 {
				rel, err := filepath.Rel(root, p)
				if err == nil {
					keep = append(keep, candidate{p, labels[i] + "/" + filepath.ToSlash(rel)})
					seen[key] = true
				}
			}
			return nil
		})
	}
	if len(keep) == 0 {
		return "", 0, nil
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].name < keep[j].name })

	ts := time.Now().Format("20060102_150405")
	f, err := os.CreateTemp(userTemp(), "CleanerPack_"+ts+"_*.zip")
	if err != nil {
		return "", 0, err
	}
	outPath := f.Name()
	complete := false
	defer func() {
		_ = f.Close()
		if !complete {
			_ = os.Remove(outPath)
		}
	}()
	zw := zip.NewWriter(f)

	// Preserve each root's relative directory structure to avoid collisions.
	count := 0
	for _, src := range keep {
		if hasReparseAncestor(src.path) {
			continue
		}
		input, err := os.Open(src.path)
		if err != nil {
			continue // occupied or unreadable files are not archived
		}
		info, err := input.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 50<<20 {
			_ = input.Close()
			continue
		}
		header, err := zip.FileInfoHeader(info)
		if err == nil {
			header.Name, header.Method = src.name, zip.Deflate
			var output io.Writer
			output, err = zw.CreateHeader(header)
			if err == nil {
				_, err = io.CopyN(output, input, info.Size())
			}
		}
		_ = input.Close()
		if err != nil {
			_ = zw.Close()
			return "", count, fmt.Errorf("打包残留文件失败: %w", err)
		}
		count++
	}
	if err := zw.Close(); err != nil {
		return "", count, err
	}
	if err := f.Close(); err != nil {
		return "", count, err
	}
	if count == 0 {
		return "", 0, nil
	}
	complete = true
	return outPath, count, nil
}
