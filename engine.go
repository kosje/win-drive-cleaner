package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
)

// Mode 描述清理模式。
type Mode int

const (
	ModeFileDelete     Mode = iota // 删除单个文件（不存在则跳过）
	ModeClearContents              // 清空目录内容（保留目录本身）
	ModeDeleteTree                 // 删除整棵目录树
	ModeThumbnailCache             // 只删除 thumbcache_*.db 文件
)

// CleanItem 一个可清理目标。
type CleanItem struct {
	Name string
	Path string
	Mode Mode
	Deep bool // 是否属于“深度清理”
}

// Result 记录一次清理结果。
type Result struct {
	Name    string
	Path    string
	Deleted int
	Bytes   int64 // 实际删除字节（dry-run 时为估算）
	Errors  int
	dryRun  bool
}

// DryRun 控制是否真正删除。
var DryRun bool

// cleanCancel 请求中止当前清理。GUI 的"停止"按钮置位此标志，
// runItems 会在**当前项做完之后**停下——绝不在删除一半的目录中途收手，
// 以免留下损坏的目录结构。
var cleanCancel atomic.Bool

// RequestCleanCancel 请求中止清理。
func RequestCleanCancel() { cleanCancel.Store(true) }

// CleanCancelled 读取中止标志。
func CleanCancelled() bool { return cleanCancel.Load() }

// ResetCleanCancel 在新一轮清理开始前清标志。
func ResetCleanCancel() { cleanCancel.Store(false) }

// standardItems 普通清理：低风险、快速、不影响系统可用性。
func standardItems() []CleanItem {
	return []CleanItem{
		{Name: "Windows 临时文件", Path: systemRoot() + "\\Temp", Mode: ModeClearContents, Deep: false},
		{Name: "用户临时文件", Path: userTemp(), Mode: ModeClearContents, Deep: false},
		{Name: "系统预读(Prefetch)", Path: filepath.Join(systemRoot(), "Prefetch"), Mode: ModeClearContents, Deep: false},
		{Name: "缩略图缓存", Path: filepath.Join(localAppData(), "Microsoft", "Windows", "Explorer"), Mode: ModeThumbnailCache, Deep: false},
		{Name: "Windows 更新下载缓存", Path: filepath.Join(systemRoot(), "SoftwareDistribution", "Download"), Mode: ModeClearContents, Deep: false},
		{Name: "应用崩溃报告(WER)", Path: filepath.Join(allUsersProfile(), "Microsoft", "Windows", "WER"), Mode: ModeClearContents, Deep: false},
	}
}

// deepItems 深度清理：空间收益大，需管理员，但仍避开裸删危险系统文件。
func deepItems() []CleanItem {
	cats := []CleanItem{
		{Name: "回收站(系统盘)", Path: recycleBin(), Mode: ModeDeleteTree, Deep: true},
		{Name: "系统盘根目录临时", Path: filepath.Join(systemDrive(), "Temp"), Mode: ModeClearContents, Deep: true},
		{Name: "旧系统备份(Windows.old)", Path: windowsOld(), Mode: ModeDeleteTree, Deep: true},
	}
	return cats
}

// collectItems 返回当前模式下要执行的条目。
func collectItems(includeDeep bool) []CleanItem {
	items := standardItems()
	if includeDeep {
		items = append(items, deepItems()...)
	}
	unique := make([]CleanItem, 0, len(items))
	for _, item := range items {
		duplicate := false
		for _, previous := range unique {
			if samePath(item.Path, previous.Path) && item.Mode == previous.Mode {
				duplicate = true
				break
			}
		}
		if !duplicate {
			unique = append(unique, item)
		}
	}
	return unique
}

// cleanItem 执行单个条目。
func cleanItem(it CleanItem) *Result {
	return cleanItemWithDryRun(it, DryRun)
}

func cleanItemWithDryRun(it CleanItem, dry bool) *Result {
	res := &Result{Name: it.Name, Path: it.Path, dryRun: dry}
	if !isPlainWindowsPath(it.Path) {
		res.Errors++
		return res
	}
	pi, err := os.Lstat(it.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			res.Errors++
		}
		return res // 不存在：正常跳过
	}
	if hasReparseAncestor(it.Path) || samePath(it.Path, filepath.Dir(it.Path)) ||
		samePath(it.Path, systemRoot()) || pathWithin(it.Path, filepath.Join(systemRoot(), "WinSxS")) ||
		pathWithin(it.Path, filepath.Join(systemRoot(), "System32")) {
		res.Errors++
		return res
	}
	resolved, err := resolvedPath(it.Path)
	if err != nil || isProtectedCleanTarget(resolved) {
		res.Errors++
		return res
	}
	switch {
	case pi.IsDir():
		switch it.Mode {
		case ModeDeleteTree:
			res.deleteTree(it.Path)
		case ModeClearContents:
			res.clearContents(it.Path)
		case ModeThumbnailCache:
			entries, err := os.ReadDir(it.Path)
			if err != nil {
				res.Errors++
				break
			}
			for _, entry := range entries {
				matched, _ := filepath.Match("thumbcache_*.db", entry.Name())
				if matched && !entry.IsDir() {
					res.deleteTree(filepath.Join(it.Path, entry.Name()))
				}
			}
		default:
			res.Errors++
		}
	default:
		res.deleteFile(it.Path, pi.Size())
	}
	return res
}

func isProtectedCleanTarget(path string) bool {
	for _, root := range append([]string{systemDrive(), systemRoot(), userProfile(), localAppData(),
		appDataDir(), allUsersProfile(), filepath.Dir(localAppData()), os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramW6432")}, mediaRoots()...) {
		if root != "" && samePath(path, resolvedPathOrOriginal(root)) {
			return true
		}
	}
	return pathWithin(path, filepath.Join(resolvedPathOrOriginal(systemRoot()), "System32")) ||
		pathWithin(path, filepath.Join(resolvedPathOrOriginal(systemRoot()), "WinSxS"))
}

func (r *Result) deleteFile(path string, size int64) {
	if !isPlainWindowsPath(path) {
		r.Errors++
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || hasReparseAncestor(path) {
		r.Errors++
		return
	}
	size = info.Size()
	if r.dryRun {
		r.Deleted++
		r.Bytes += size
		return
	}
	// 删前先记日志（可追溯）
	logDeletion(path, size)
	if err := os.Remove(path); err != nil {
		r.Errors++
		return
	}
	r.Deleted++
	r.Bytes += size
}

func (r *Result) clearContents(dir string) {
	if hasReparseAncestor(dir) {
		r.Errors++
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		r.Errors++
		return
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		r.deleteTree(full)
	}
}

func (r *Result) deleteTree(dir string) {
	if !isPlainWindowsPath(dir) {
		r.Errors++
		return
	}
	info, err := os.Lstat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			r.Errors++
		}
		return
	}
	if hasReparseAncestor(dir) {
		r.Errors++
		return
	}
	if !info.IsDir() {
		r.deleteFile(dir, info.Size())
		return
	}
	// Delete bottom-up, counting only successful removals. RemoveAll can
	// partially succeed, making a subsequent size estimate overcount bytes.
	r.clearContents(dir)
	if r.dryRun {
		r.Deleted++
		return
	}
	logDeletion(dir, 0)
	if err := os.Remove(dir); err != nil {
		r.Errors++
		return
	}
	r.Deleted++
}

func walkSize(root string) (int64, int64) {
	var count, bytes int64
	safeWalk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		count++
		if !info.IsDir() {
			bytes += info.Size()
		}
		return nil
	})
	return count, bytes
}

// OnProgress 每完成一个清理项时被调用，用于实时逐条输出进度。
// 由调用方（main）在 runItems 前设置；为 nil 时不输出。
var OnProgress func(seq int, total int, it CleanItem, res *Result)

// runItems 按模式执行全部条目，逐条触发 OnProgress（实时可见进度）。
// 每项**做完之后**检查中止标志：保证不会在删除一半的目录中途收手。
func runItems(includeDeep bool) []*Result {
	ResetCleanCancel()
	return runItemsWithOptions(includeDeep, DryRun, OnProgress)
}

func runItemsWithOptions(includeDeep, dry bool, progress func(int, int, CleanItem, *Result)) []*Result {
	items := collectItems(includeDeep)
	out := make([]*Result, 0, len(items))
	total := len(items)
	for i, it := range items {
		if CleanCancelled() {
			break
		}
		res := cleanItemWithDryRun(it, dry)
		out = append(out, res)
		if progress != nil {
			progress(i+1, total, it, res)
		}
		if CleanCancelled() {
			// 已完成当前项，安全停下；剩余项不执行
			break
		}
	}
	return out
}

// totals 汇总。
func totalsOf(r []*Result) (deleted, errs int, bytes int64) {
	for _, x := range r {
		deleted += x.Deleted
		errs += x.Errors
		bytes += x.Bytes
	}
	return
}
