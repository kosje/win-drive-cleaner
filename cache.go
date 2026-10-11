package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// CacheEntry 一个软件在白名单中的可重新生成缓存目录及其大小。
type CacheEntry struct {
	Software string // 软件名（目录名）
	Path     string // 目录完整路径
	Bytes    int64
}

// scanCache 仅统计白名单中的已知缓存目录，
// 只保留 size >= minBytes 且非系统关键目录的项，按大小降序。
func scanCache(minBytes int64) []CacheEntry {
	out, _ := scanCacheControlled(minBytes, nil)
	return out
}

func scanCacheControlled(minBytes int64, control *scanControl) ([]CacheEntry, error) {
	seen := map[string]bool{}
	var out []CacheEntry
	defer control.flush()
	for _, rule := range cacheRules() {
		if err := control.check(); err != nil {
			return nil, err
		}
		paths, err := filepath.Glob(filepath.Join(rule.root, rule.pattern))
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			key := strings.ToLower(filepath.Clean(p))
			if seen[key] || !isCacheDeletable(p) {
				continue
			}
			seen[key] = true
			var b int64
			err := control.walk(p, func(_ string, info os.FileInfo, err error) error {
				if err == nil && info.Mode().IsRegular() {
					b += info.Size()
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if b < minBytes {
				continue
			}
			out = append(out, CacheEntry{Software: rule.software, Path: p, Bytes: b})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Path < out[j].Path
	})
	return out, control.check()
}

// printCacheScan 显示 Top N 缓存目录（N<=0 时默认 15）。
func printCacheScan(top int) {
	if top <= 0 {
		top = 15
	}
	fmt.Println()
	fmt.Println("  ── 软件缓存（仅白名单中的可重新生成缓存）──")
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
	fmt.Println("  （仅清理列出的缓存内容，保留软件配置、登录数据及缓存目录；建议先关闭对应软件）")
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
		fmt.Println("  将要清理以下缓存目录的内容（建议先关闭对应软件）:")
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
			res.clearContents(e.Path)
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
	parts := strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == ',' || r == '，' || r == ';' })
	for _, part := range parts {
		part = strings.TrimPrefix(strings.ToLower(part), "m")
		if n, err := strconv.Atoi(part); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}
