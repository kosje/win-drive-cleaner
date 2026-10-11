package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CacheEntry 一个软件在 AppData 下的缓存/数据目录及其大小。
type CacheEntry struct {
	Software string // 软件名（目录名）
	Path     string // 目录完整路径
	Bytes    int64
}

// cacheRoots 扫描 AppData 下常见会堆积缓存的软件目录。
// 只列大小供用户判断，不自动删；用户勾选后才删。
func cacheRoots() []string {
	local := localAppData()
	roaming := appDataDir()
	return []string{local, roaming}
}

// scanCache 扫描两个 AppData 根目录的**直接子目录**，统计每个子目录总大小，
// 只保留 size >= minBytes 且非系统关键目录的项，按大小降序。
func scanCache(minBytes int64) []CacheEntry {
	seen := map[string]bool{}
	var out []CacheEntry
	for _, root := range cacheRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			p := filepath.Join(root, name)
			key := strings.ToLower(filepath.Clean(p))
			if seen[key] || hasReparseAncestor(p) {
				continue
			}
			seen[key] = true
			// 跳过系统关键目录
			if isSystemCacheDir(name) {
				continue
			}
			n, b := walkSize(p)
			_ = n
			if b < minBytes {
				continue
			}
			out = append(out, CacheEntry{Software: name, Path: p, Bytes: b})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Software < out[j].Software
	})
	return out
}

// isSystemCacheDir 跳过 Windows/系统自己维护、删了会出问题的目录。
func isSystemCacheDir(name string) bool {
	low := strings.ToLower(name)
	switch low {
	case "microsoft", "packages", "microsoft edge", "msedge", "google",
		"kaspersky", "endpoint protection", "one drive", "onedrive", "windows", "temp", "windrivecleaner":
		// 系统/核心/已单独处理目录，不让用户误删
		return true
	}
	return false
}

// printCacheScan 显示 Top N 缓存目录（N<=0 时默认 15）。
func printCacheScan(top int) {
	if top <= 0 {
		top = 15
	}
	fmt.Println()
	fmt.Println("  ── AppData 软件缓存（按大小 Top，勾选删除）──")
	list := scanCache(10 << 20) // 只显示 >= 10MB 的，太小无意义
	if len(list) == 0 {
		fmt.Println("   未发现 10MB 以上的 AppData 软件缓存目录。")
		return
	}
	if len(list) > top {
		list = list[:top]
	}
	printCacheEntries(list)
}

func printCacheEntries(list []CacheEntry) {
	for i, e := range list {
		fmt.Printf("  M%02d  %-16s %10s  %s\n", i+1, e.Software, formatBytes(e.Bytes), e.Path)
	}
	fmt.Println("  （M 序号用于勾选删除；删的是该软件的缓存/数据目录，可能丢失其登录态/设置）")
}

// cacheIndex 与 mediaIndex 类似：保存本轮扫描结果供用户按序号操作。
var cacheIndex []CacheEntry

// cacheFlow 交互流程：扫描显示 → 用户输入要删的 M 序号（逗号/空格分隔）→ 逐个确认删除。
func cacheFlow() {
	DryRun = false
	cacheIndex = scanCache(10 << 20)
	if len(cacheIndex) > 15 {
		cacheIndex = cacheIndex[:15]
	}
	if len(cacheIndex) == 0 {
		return
	}
	for {
		printCacheEntries(cacheIndex)
		fmt.Println()
		fmt.Print("  输入要删除的 M 序号(如 1,3,5 或 m1 m3，q=返回): ")
		c, err := readInputLine()
		if err != nil {
			return
		}
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "q" || c == "" {
			return
		}
		ids := parseInts(c)
		var valid []int
		seen := map[int]bool{}
		for _, id := range ids {
			if id >= 1 && id <= len(cacheIndex) && !seen[id] {
				valid = append(valid, id)
				seen[id] = true
			}
		}
		if len(valid) == 0 {
			fmt.Println("  未识别有效序号。")
			continue
		}
		// 先汇总要删的内容给用户看清
		var total int64
		fmt.Println()
		fmt.Println("  将要删除以下软件目录（不可恢复，登录态/设置会丢失）:")
		for _, id := range valid {
			e := cacheIndex[id-1]
			total += e.Bytes
			fmt.Printf("   M%02d  %-16s %s  %s\n", id, e.Software, formatBytes(e.Bytes), e.Path)
		}
		if !askYesNo(fmt.Sprintf("  确定删除以上共 %s 的软件目录吗？", formatBytes(total)), false) {
			fmt.Println("  已取消，未删除任何软件目录。")
			continue
		}
		for _, id := range valid {
			e := cacheIndex[id-1]
			if !isCacheDeletable(e.Path) {
				fmt.Printf("   路径已失效，跳过: %s\n", e.Path)
				continue
			}
			res := &Result{Name: "软件缓存", Path: e.Path}
			res.deleteTree(e.Path)
			note := ""
			if res.Errors > 0 {
				note = fmt.Sprintf("（%d 项因占用未删）", res.Errors)
			}
			fmt.Printf("   已清理 %s，释放 %s%s\n", e.Software, formatBytes(res.Bytes), note)
		}
		// 删完后重新扫描供继续操作
		cacheIndex = scanCache(10 << 20)
		if len(cacheIndex) > 15 {
			cacheIndex = cacheIndex[:15]
		}
		if len(cacheIndex) == 0 {
			fmt.Println("  已无 10MB 以上缓存目录。")
			return
		}
	}
}

// CacheFlowReadOnly 只读扫描（--scan-cache 用）。
func CacheFlowReadOnly() {
	printCacheScan(15)
}

// trimLower 去空格 + 小写。
func trimLower(s string) string {
	var b []rune
	for _, r := range s {
		switch r {
		case ' ', '\t':
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r = r - 'A' + 'a'
		}
		b = append(b, r)
	}
	return string(b)
}

// parseInts 从 "1,3,5 m7" 之类的串里抽出所有正整数。
func parseInts(s string) []int {
	var out []int
	cur := 0
	any := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			cur = cur*10 + int(r-'0')
			any = true
			continue
		}
		if any {
			out = append(out, cur)
			cur = 0
			any = false
		}
	}
	if any {
		out = append(out, cur)
	}
	return out
}
