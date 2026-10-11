package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// samePath and pathWithin use Windows' case-insensitive path semantics.
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func pathWithin(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return false
	}
	return samePath(path, root) || strings.HasPrefix(strings.ToLower(path),
		strings.ToLower(strings.TrimRight(root, `\`)+string(filepath.Separator)))
}

func isReparsePoint(info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// Check ancestors too: Lstat(file) alone does not detect a parent junction.
func hasReparseAncestor(path string) bool {
	if !filepath.IsAbs(path) {
		return true
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || isReparsePoint(info) {
			return true
		}
		if samePath(p, filepath.Dir(p)) {
			return false
		}
	}
}

// safeWalk never follows junctions, symbolic links, or other reparse points.
func safeWalk(root string, fn filepath.WalkFunc) error {
	if hasReparseAncestor(root) {
		return nil
	}
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && isReparsePoint(info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return fn(path, info, err)
	})
}
