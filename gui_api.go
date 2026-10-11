//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// GUI 桥接层：把 Go 侧的清理/扫描能力暴露给前端。
// 约定：快操作同步返回；耗时操作一律走 guiApp.async + window.__wdcResult 异步回传。

// ---------- 磁盘信息 ----------

// diskUsage 返回系统盘剩余/总字节。
func diskUsage() (free, total int64) {
	path := systemDrive() // 例如 C:
	var fr, tot, avail uint64
	if err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(path), &fr, &tot, &avail); err == nil {
		return int64(fr), int64(tot)
	}
	return freeSpaceOnSystemDrive(), 0
}

// apiDiskInfo 供前端读取磁盘与管理员状态。
func apiDiskInfo() (map[string]interface{}, error) {
	free, total := diskUsage()
	return map[string]interface{}{
		"free":         free,
		"total":        total,
		"admin":        isAdmin(),
		"compactable":  compactSupported(),
		"systemDrive":  systemDrive(),
	}, nil
}

// ---------- 开机自启 ----------

// apiStartupList 返回全部开机自启项视图。
func apiStartupList() ([]map[string]interface{}, error) {
	items := enumerateStartups()
	admin := isAdmin()
	out := make([]map[string]interface{}, 0, len(items))
	for _, e := range items {
		out = append(out, map[string]interface{}{
			"id":        e.ID,
			"name":      e.Name,
			"target":    e.Target,
			"source":    e.Source.String(),
			"enabled":   e.Enabled,
			"needAdmin": e.Source.isLMHive() && !admin,
		})
	}
	return out, nil
}

// apiStartupToggle 对单条自启项执行 启用/禁用。
func apiStartupToggle(id string, on bool) (map[string]interface{}, error) {
	if id == "" {
		return map[string]interface{}{"ok": false, "error": "缺少自启项标识"}, nil
	}
	// HKLM 项在非管理员下必然失败，前置拦截以给出明确原因（而不是抛底层注册表错误）
	if strings.HasPrefix(id, "HKLM:") && !isAdmin() {
		return map[string]interface{}{
			"ok":    false,
			"error": "该项位于「所有用户(HKLM)」，需以管理员身份运行本工具才能修改",
		}, nil
	}
	items := enumerateStartups()
	for i := range items {
		if items[i].ID == id {
			res := setValueEnabled(&items[i], on)
			return map[string]interface{}{"ok": res.OK, "msg": res.Msg, "error": res.Error}, nil
		}
	}
	return map[string]interface{}{"ok": false, "error": "未找到该自启项"}, nil
}

// ---------- 清理 ----------

// runCleanJob 执行一次磁盘清理，逐项把进度推给前端。
func runCleanJob(app *guiApp, deep, dry bool) (interface{}, error) {
	ResetCleanCancel()
	DryRun = dry

	freeBefore := freeSpaceOnSystemDrive()
	cancelled := false

	OnProgress = func(seq, total int, it CleanItem, res *Result) {
		app.pushResult("cleanProgress", map[string]interface{}{
			"seq":     seq,
			"total":   total,
			"name":    it.Name,
			"path":    it.Path,
			"deleted": res.Deleted,
			"bytes":   res.Bytes,
			"errors":  res.Errors,
		}, nil)
		cancelled = CleanCancelled()
	}

	results := runItems(deep)
	OnProgress = nil

	deleted, errs, bytes := totalsOf(results)
	freeAfter := freeSpaceOnSystemDrive()

	actual := int64(0)
	if !dry && freeBefore > 0 && freeAfter > 0 && freeAfter > freeBefore {
		actual = freeAfter - freeBefore
	}

	return map[string]interface{}{
		"deleted":    deleted,
		"errs":       errs,
		"bytes":      bytes,
		"dry":        dry,
		"deep":       deep,
		"cancelled":  cancelled,
		"freeBefore": freeBefore,
		"freeAfter":  freeAfter,
		"actualGain": actual,
	}, nil
}

// apiCancelClean 请求中止当前清理（在下一个清理项开始前生效）。
func apiCancelClean() (map[string]interface{}, error) {
	RequestCleanCancel()
	return map[string]interface{}{"ok": true}, nil
}

// apiElevate 以管理员身份重新拉起本工具的 GUI（用于深度清理 / DISM / 压缩）。
func apiElevate() (map[string]interface{}, error) {
	if isAdmin() {
		return map[string]interface{}{"ok": false, "msg": "当前已是管理员"}, nil
	}
	if err := elevateSelfWithArgs("--gui"); err != nil {
		return map[string]interface{}{"ok": false, "msg": err.Error()}, nil
	}
	return map[string]interface{}{"ok": true, "msg": "已请求管理员权限，新窗口将打开；请关闭本窗口。"}, nil
}

// ---------- 通讯软件缓存 ----------

// scanCommsJob 统计各通讯软件本地目录大小（只读）。
func scanCommsJob() (interface{}, error) {
	targets := commsTargets()
	out := make([]map[string]interface{}, 0, len(targets))
	for _, t := range targets {
		var total int64
		exists := make([]string, 0, len(t.Roots))
		for _, root := range t.Roots {
			if st, err := os.Stat(root); err == nil && st.IsDir() {
				exists = append(exists, root)
				_, b := walkSize(root)
				total += b
			}
		}
		out = append(out, map[string]interface{}{
			"name":   t.Name,
			"roots":  exists,
			"bytes":  total,
			"exists": len(exists) > 0,
		})
	}
	return map[string]interface{}{"items": out}, nil
}

// ---------- AppData 软件缓存 ----------

// scanCacheJob 列出 AppData 下 ≥10MB 的软件目录（只读）。
func scanCacheJob() (interface{}, error) {
	entries := scanCache(10 << 20)
	out := make([]map[string]interface{}, 0, len(entries))
	var total int64
	for _, e := range entries {
		out = append(out, map[string]interface{}{
			"software": e.Software,
			"path":     e.Path,
			"bytes":    e.Bytes,
		})
		total += e.Bytes
	}
	return map[string]interface{}{"items": out, "total": total}, nil
}

// isCacheDeletable 校验目标路径确实位于 AppData 扫描根目录的**直接子目录**内，
// 且不是被保护的系统目录。
// 这是删除操作唯一的准入闸门：前端只能提交 scanCache 返回过的路径，
// 不能自行构造任意路径，避免误删 AppData 之外的文件。
func isCacheDeletable(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)
	// 必须是绝对路径
	if !filepath.IsAbs(clean) {
		return false
	}
	for _, root := range cacheRoots() {
		rootClean := filepath.Clean(root)
		// 只接受根目录的**直接子目录**，杜绝 "..\\..\\Windows" 之类的穿越
		if filepath.Dir(clean) != rootClean {
			continue
		}
		name := filepath.Base(clean)
		if name == "" || name == "." || name == ".." {
			return false
		}
		if isSystemCacheDir(name) {
			return false
		}
		return true
	}
	return false
}

// deleteCacheJob 按**路径**删除软件缓存目录。
//
// 注意：早期实现用"序号 + 后端重新扫描"定位，会因两次扫描间目录大小变化
// 而删错目录（排序基于大小，扫描结果不稳定）。现改为前端回传完整路径、
// 后端做白名单校验，彻底消除错位风险。
func deleteCacheJob(paths []string) (interface{}, error) {
	if len(paths) == 0 {
		return nil, errors.New("未选择要删除的目录")
	}

	// 先整体校验：只要有一个不合法就整体拒绝，避免"删一半"让用户困惑
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		if !isCacheDeletable(p) {
			return nil, fmt.Errorf("路径不在允许清理的范围内，已取消全部操作: %s", p)
		}
		c := filepath.Clean(p)
		// 去重
		dup := false
		for _, e := range cleaned {
			if e == c {
				dup = true
				break
			}
		}
		if !dup {
			cleaned = append(cleaned, c)
		}
	}

	var deleted int
	var errs int
	var freed int64
	items := make([]map[string]interface{}, 0, len(cleaned))

	for _, p := range cleaned {
		size := int64(0)
		_, size = walkSize(p)
		// 删前写审计日志（与 CLI 路径保持一致，可追溯）
		logDeletion(p, size)

		res := &Result{Name: "软件缓存", Path: p}
		res.deleteTree(p)
		deleted += res.Deleted
		errs += res.Errors
		freed += res.Bytes
		items = append(items, map[string]interface{}{
			"path":     p,
			"name":     filepath.Base(p),
			"bytes":    res.Bytes,
			"deleted":  res.Deleted,
			"errors":   res.Errors,
		})
	}

	return map[string]interface{}{
		"deleted": deleted,
		"errors":  errs,
		"bytes":   freed,
		"items":   items,
	}, nil
}

// ---------- 大文件 ----------

// scanBigFilesJob 扫描系统盘 ≥500MB 的文件 Top 20（只列不删）。
func scanBigFilesJob() (interface{}, error) {
	list := scanBigFiles(500, 20)
	out := make([]map[string]interface{}, 0, len(list))
	var total int64
	for _, f := range list {
		out = append(out, map[string]interface{}{"path": f.Path, "bytes": f.Bytes})
		total += f.Bytes
	}
	return map[string]interface{}{"items": out, "total": total}, nil
}

// deleteBigFilesJob 按路径删除用户勾选的大文件。
//
// 与 deleteCacheJob 同理：前端回传完整路径，后端用 isBigFileDeletable 做
// 白名单校验（必须位于系统盘、非系统目录、非保留文件、是普通文件），
// 任一路径非法即整体拒绝，杜绝删错 / 删除系统文件。
func deleteBigFilesJob(paths []string) (interface{}, error) {
	if len(paths) == 0 {
		return nil, errors.New("未选择要删除的文件")
	}

	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		if !isBigFileDeletable(p) {
			return nil, fmt.Errorf("路径不在允许删除的范围内，已取消全部操作: %s", p)
		}
		c := filepath.Clean(p)
		dup := false
		for _, e := range cleaned {
			if e == c {
				dup = true
				break
			}
		}
		if !dup {
			cleaned = append(cleaned, c)
		}
	}

	var deleted, errs int
	var freed int64
	items := make([]map[string]interface{}, 0, len(cleaned))

	for _, p := range cleaned {
		var size int64
		if st, err := os.Stat(p); err == nil {
			size = st.Size()
		}
		logDeletion(p, size)

		res := &Result{Name: "大文件", Path: p}
		if DryRun {
			res.Deleted = 1
			res.Bytes = size
		} else {
			res.deleteFile(p, size)
		}
		deleted += res.Deleted
		errs += res.Errors
		freed += res.Bytes
		items = append(items, map[string]interface{}{
			"path":    p,
			"name":    filepath.Base(p),
			"bytes":   res.Bytes,
			"deleted": res.Deleted,
			"errors":  res.Errors,
		})
	}

	return map[string]interface{}{
		"deleted": deleted,
		"errors":  errs,
		"bytes":   freed,
		"items":   items,
	}, nil
}

// ---------- 视频 / 录屏 ----------

// scanMediaJob 收集常见目录下的视频候选文件（只读）。
func scanMediaJob() (interface{}, error) {
	list := scanMedia()
	out := make([]map[string]interface{}, 0, len(list))
	var total int64
	for _, m := range list {
		out = append(out, map[string]interface{}{"path": m.Path, "size": m.Size})
		total += m.Size
	}
	return map[string]interface{}{"items": out, "total": total}, nil
}

// ---------- 清理审计日志 ----------

// apiReadLog 读取最近的删除审计日志（默认尾部 400 行）。
// 只读操作，但文件可能很大，故限制读取量并放到异步任务里。
func apiReadLog(lines int) (map[string]interface{}, error) {
	if lines <= 0 || lines > 5000 {
		lines = 400
	}
	dir := logDir()
	files, err := filepath.Glob(filepath.Join(dir, "CleanerLog_*.txt"))
	if err != nil || len(files) == 0 {
		return map[string]interface{}{
			"dir": dir, "files": []string{}, "content": "（尚无清理日志）", "count": 0,
		}, nil
	}
	// 取最近修改的那个
	latest := files[0]
	var latestTime time.Time
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			if st.ModTime().After(latestTime) {
				latestTime = st.ModTime()
				latest = f
			}
		}
	}
	data, err := os.ReadFile(latest)
	if err != nil {
		return nil, err
	}
	content := string(data)
	// 截取尾部若干行
	rows := strings.Split(content, "\n")
	tail := 0
	for i := len(rows) - 1; i >= 0 && tail < lines; i-- {
		if strings.TrimSpace(rows[i]) != "" {
			tail++
		}
	}
	if tail < len(rows) {
		content = strings.Join(rows[len(rows)-tail:], "\n")
	}
	return map[string]interface{}{
		"dir":     dir,
		"file":    filepath.Base(latest),
		"count":   tail,
		"content": content,
	}, nil
}

// ---------- 资源管理器 ----------

// apiOpenFolder 在资源管理器中打开指定路径。
func apiOpenFolder(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("路径为空")
	}
	return OpenInExplorer(path)
}