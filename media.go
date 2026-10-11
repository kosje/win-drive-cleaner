package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MediaEntry 一个候选视频/录屏文件。
type MediaEntry struct {
	Path string
	Size int64
}

// videoExts 常见视频扩展名。
var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true,
	".webm": true, ".m4v": true, ".flv": true, ".wmv": true,
}

// mediaRoots 视频/录屏可能出现的目录（只读扫描）。
func mediaRoots() []string {
	up := userProfile()
	roots := []string{
		filepath.Join(up, "Videos"),
		filepath.Join(up, "Desktop"),
		filepath.Join(up, "Downloads"),
		filepath.Join(up, "Documents"),
		// Xbox GameDVR 录屏
		filepath.Join(localAppData(), "Microsoft", "GameDVR"),
	}
	return uniqueStrings(roots)
}

// scanMedia 收集所有候选视频文件（不递归进子目录太多层，避免慢）。
func scanMedia() []MediaEntry {
	var out []MediaEntry
	for _, root := range mediaRoots() {
		// 仅扫描该根目录两层，控制耗时
		safeWalk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if !videoExts[ext] {
				return nil
			}
			out = append(out, MediaEntry{Path: p, Size: info.Size()})
			return nil
		})
	}
	// 按大小降序
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}

// printMediaScan 提示候选视频/录屏文件（Top N），并说明可打开所在文件夹手动清理。
func printMediaScan(top int) {
	mediaIndex = nil
	mediaDirs = nil
	files := scanMedia()
	if len(files) == 0 {
		fmt.Println("  未在常见目录发现视频/录屏文件。")
		return
	}
	if top <= 0 {
		top = 20
	}
	if top > len(files) {
		top = len(files) // clamp 到实际数量，避免 files[i] 越界 panic 导致闪退
	}
	mediaIndex = nil
	mediaDirs = nil
	fmt.Println()
	fmt.Printf("  ── 视频 / 录屏（%d 个候选，按大小 Top %d）──\n", len(files), top)
	for i := 0; i < top; i++ {
		f := files[i]
		dir := filepath.Dir(f.Path)
		if i < 10 {
			tag := fmt.Sprintf("M%02d", i+1)
			fmt.Printf("   %s  %s  %s\n", tag, formatBytes(f.Size), f.Path)
			mediaIndex = append(mediaIndex, f)
		} else {
			fmt.Printf("      %s  %s\n", formatBytes(f.Size), f.Path)
		}
		mediaDirs = append(mediaDirs, dir)
	}
}

// mediaIndex / mediaDirs 供“打开所在文件夹”用（保存 Top 10 的映射）。
var (
	mediaIndex []MediaEntry
	mediaDirs  []string
)

// openMediaFolder 按 1 基序号打开某候选视频所在文件夹（资源管理器选中该文件）。
func openMediaFolder(idx int) {
	if idx < 1 || idx > len(mediaIndex) {
		return
	}
	_ = OpenInExplorer(mediaIndex[idx-1].Path)
}
