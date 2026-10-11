package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CommsTarget 一个通讯软件的本地文件/缓存目录。
type CommsTarget struct {
	Name  string
	Roots []string // 可能存在多个位置
}

// commsTargets 微信 / QQ / 企业微信 常见本地缓存与历史文件目录。
func commsTargets() []CommsTarget {
	roaming := appDataDir()
	up := userProfile()
	docs := filepath.Join(up, "Documents")
	return []CommsTarget{
		{
			Name: "微信 WeChat",
			Roots: []string{
				filepath.Join(roaming, "Tencent", "WeChat", "Files"),
				filepath.Join(roaming, "Tencent", "WeChat", "WXStorage"),
				filepath.Join(docs, "WeChat Files"),
			},
		},
		{
			Name: "QQ",
			Roots: []string{
				filepath.Join(roaming, "Tencent", "QQ"),
				filepath.Join(roaming, "Tencent", "QQNT"),
				filepath.Join(localAppData(), "Tencent", "QQ"),
				filepath.Join(docs, "Tencent Files"), // 新版 QQ 数据在 Documents\Tencent Files
			},
		},
		{
			Name: "企业微信 WeCom",
			Roots: []string{
				filepath.Join(roaming, "Tencent", "WeWork"),
				filepath.Join(roaming, "WXWork"),
				filepath.Join(docs, "WXWork"),
			},
		},
	}
}

// appDataDir 返回 %APPDATA%（Roaming）。
func appDataDir() string {
	if v := strings.TrimSpace(os.Getenv("APPDATA")); v != "" {
		return v
	}
	return filepath.Join(userProfile(), "AppData", "Roaming")
}

// commsSizeOlder 统计某通讯软件在 days 天前文件的总大小与数量。
func commsSizeOlder(t CommsTarget, days int) (int64, int, bool) {
	var total int64
	var count int
	found := false
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, root := range t.Roots {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			found = true
		}
		safeWalk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if info.ModTime().Before(cutoff) {
				total += info.Size()
				count++
			}
			return nil
		})
	}
	return total, count, found
}

// deleteCommsOlder 删除某通讯软件 days 天前的文件，返回释放字节、条数、错误数。
func deleteCommsOlder(t CommsTarget, days int) (int64, int64, int64) {
	var bytes, count, errs int64
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, root := range t.Roots {
		safeWalk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if info.ModTime().Before(cutoff) {
				if DryRun {
					bytes += info.Size()
					count++
					return nil
				}
				logDeletion(p, info.Size())
				if err := os.Remove(p); err != nil {
					errs++
					return nil
				}
				bytes += info.Size()
				count++
			}
			return nil
		})
	}
	return bytes, count, errs
}

// PrintCommsScan 列出每个通讯软件可清理的大小（不删除）。
func PrintCommsScan(days int) {
	fmt.Println()
	fmt.Printf("  ── 通讯软件（清理 %d 天前的文件）──\n", days)
	for _, t := range commsTargets() {
		b, n, found := commsSizeOlder(t, days)
		if !found {
			fmt.Printf("   %-18s 未找到本地缓存目录\n", t.Name)
			continue
		}
		if b == 0 {
			fmt.Printf("   %-18s 无 %d 天前的文件\n", t.Name, days)
			continue
		}
		fmt.Printf("   %-18s 可清理 %s（%d 个旧文件）\n", t.Name, formatBytes(b), n)
	}
}
