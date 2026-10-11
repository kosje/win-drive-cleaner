package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// Reject Win32 aliases that filepath.Clean does not normalize, such as
// Windows. and alternate data streams. Device namespaces are not cleanup paths.
func isPlainWindowsPath(path string) bool {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || strings.HasPrefix(clean, `\\?\`) || strings.HasPrefix(clean, `\\.\`) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(clean, filepath.VolumeName(clean)), `\`)
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.TrimRight(part, " .") != part || strings.ContainsAny(part, ":\x00") {
			return false
		}
	}
	return true
}

// Resolve existing files using their Windows handle, including 8.3 aliases.
func resolvedPath(path string) (string, error) {
	if !isPlainWindowsPath(path) {
		return "", fmt.Errorf("不支持的清理路径: %s", path)
	}
	name, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if n >= uint32(len(buf)) {
			buf = make([]uint16, n+1)
			continue
		}
		final := windows.UTF16ToString(buf[:n])
		if strings.HasPrefix(final, `\\?\UNC\`) {
			final = `\\` + strings.TrimPrefix(final, `\\?\UNC\`)
		} else {
			final = strings.TrimPrefix(final, `\\?\`)
		}
		return filepath.Clean(final), nil
	}
}

func resolvedPathOrOriginal(path string) string {
	if resolved, err := resolvedPath(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

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
