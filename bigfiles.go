package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// BigFile 一个超过阈值的大文件。
type BigFile struct {
	Path  string
	Bytes int64
}

// bigFileExcludedDirs 不应遍历/列出的目录（相对系统盘根的后缀匹配）。
func bigFileExcludedDirs() []string {
	root := systemDrive()
	return []string{
		systemRoot(),
		filepath.Join(root, "Windows"),
		filepath.Join(root, "Program Files"),
		filepath.Join(root, "Program Files (x86)"),
		filepath.Join(root, "ProgramData"),
		filepath.Join(root, "$Recycle.Bin"),
		filepath.Join(root, "System Volume Information"),
		filepath.Join(root, "Recovery"),
		filepath.Join(root, "PerfLogs"),
	}
}

// bigFileExcludedFiles 系统保留文件，不是"垃圾"。
func bigFileExcludedFiles() map[string]bool {
	return map[string]bool{
		"pagefile.sys": true, "swapfile.sys": true, "hiberfil.sys": true,
		"dumpstack.log.tmp": true, "ntds.dit": true, "usnjournal.log": true,
	}
}

// scanBigFiles 扫描系统盘，找出 >= minMB 的文件（排除系统目录/保留文件），按大小降序取 Top N。
// 只列不删——这些文件（误下的大文件/旧安装包/视频）往往才是 C 盘最大的占用。
func scanBigFiles(minMB, top int) []BigFile {
	if minMB <= 0 {
		minMB = 500
	}
	if top <= 0 {
		top = 20
	}
	minBytes := int64(minMB) << 20
	root := systemDrive()
	exclDirs := bigFileExcludedDirs()
	exclFiles := bigFileExcludedFiles()

	isExcludedDir := func(p string) bool {
		clean := filepath.Clean(p)
		for _, ex := range exclDirs {
			if pathWithin(clean, ex) {
				return true
			}
		}
		return false
	}

	var out []BigFile
	safeWalk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			// 无权限目录：若是目录且未排除则剪枝；否则跳过本节点
			if info != nil && info.IsDir() && isExcludedDir(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			if isExcludedDir(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() || exclFiles[strings.ToLower(info.Name())] {
			return nil
		}
		if info.Size() >= minBytes {
			out = append(out, BigFile{Path: p, Bytes: info.Size()})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	if len(out) > top {
		out = out[:top]
	}
	return out
}

// printBigFiles 显示 Top N 大文件。
func printBigFiles(minMB, top int) {
	list := scanBigFiles(minMB, top)
	printBigFileEntries(list, minMB, top)
}

func printBigFileEntries(list []BigFile, minMB, top int) {
	fmt.Println()
	fmt.Printf("  ── 大文件检测（≥ %dMB，Top %d，只列不删）──\n", minMB, top)
	if len(list) == 0 {
		fmt.Printf("   未发现 ≥ %dMB 的文件（已排除系统目录/保留文件）。\n", minMB)
		return
	}
	for i, f := range list {
		fmt.Printf("  B%02d  %12s  %s\n", i+1, formatBytes(f.Bytes), f.Path)
	}
	fmt.Println("  （这些往往是误下的大文件/旧安装包/视频；B 序号可打开所在文件夹手动处理）")
}

// bigIndex 保存本轮扫描结果。
var bigIndex []BigFile

// bigFileFlow 交互流程：问阈值 → 扫描显示 → 用户按 B 序号打开所在文件夹。
func bigFileFlow() {
	minMB := askInt("  检测 ≥ 多少 MB 的大文件？(默认 500): ", 500)
	if minMB <= 0 {
		minMB = 500
	}
	bigIndex = scanBigFiles(minMB, 20)
	printBigFileEntries(bigIndex, minMB, 20)
	if len(bigIndex) == 0 {
		return
	}
	for {
		fmt.Println()
		fmt.Printf("  输入 B 序号(1-%d)打开所在文件夹，q=返回: ", len(bigIndex))
		var c string
		_, _ = fmt.Scanln(&c)
		c = trimLower(c)
		if c == "q" || c == "" {
			return
		}
		n, err := strconv.Atoi(c)
		if err != nil || n < 1 || n > len(bigIndex) {
			fmt.Println("  未识别有效序号。")
			continue
		}
		_ = OpenInExplorer(filepath.Dir(bigIndex[n-1].Path))
	}
}

// BigFilesFlowReadOnly 只读（--scan-bigfiles 用）。
func BigFilesFlowReadOnly() {
	printBigFiles(500, 20)
}

// isBigFileDeletable 校验目标是否为可安全删除的大文件。
// 准入条件（与扫描排除规则一致，防止误删系统文件）：
//   - 必须是绝对路径，且位于系统盘内
//   - 不在 bigFileExcludedDirs 排除目录（Windows/Program Files/ProgramData 等）
//   - 不是系统保留文件（pagefile.sys / hiberfil.sys 等）
//   - 必须是真实存在的普通文件（非目录、非软链）
func isBigFileDeletable(path string) bool {
	clean := filepath.Clean(path)
	if clean == "" || !filepath.IsAbs(clean) {
		return false
	}
	root := filepath.Clean(systemDrive())
	if !pathWithin(clean, root) {
		return false
	}
	for _, ex := range bigFileExcludedDirs() {
		exClean := filepath.Clean(ex)
		if pathWithin(clean, exClean) {
			return false
		}
	}
	if bigFileExcludedFiles()[strings.ToLower(filepath.Base(clean))] {
		return false
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.Mode().IsRegular() || hasReparseAncestor(clean) {
		return false
	}
	return true
}
