package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// Read backwards in blocks rather than loading a potentially huge log.
func readLogTail(f *os.File, lines int) (string, int, error) {
	if lines <= 0 {
		return "", 0, nil
	}
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	var blocks [][]byte
	read, newlines := 0, 0
	pos := info.Size()
	const maxBytes = 4 << 20
	for pos > 0 && read < maxBytes {
		n := int64(4096)
		if n > pos {
			n = pos
		}
		pos -= n
		block := make([]byte, n)
		nread, err := f.ReadAt(block, pos)
		if err != nil && err != io.EOF {
			return "", 0, err
		}
		blocks = append(blocks, block[:nread])
		read += int(n)
		newlines += bytes.Count(block[:nread], []byte{'\n'})
		if newlines > lines {
			break
		}
	}
	var buffer strings.Builder
	buffer.Grow(read)
	for i := len(blocks) - 1; i >= 0; i-- {
		buffer.Write(blocks[i])
	}
	text := buffer.String()
	if text == "" {
		return "", 0, nil
	}
	if pos > 0 {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
			if text == "" {
				return "", 0, fmt.Errorf("日志末尾单行超过 4MB，请在文本编辑器中查看")
			}
		} else {
			return "", 0, fmt.Errorf("日志末尾单行超过 4MB，请在文本编辑器中查看")
		}
	}
	// Remove one delimiter, preserving genuine blank lines at the end.
	text = strings.TrimSuffix(text, "\n")
	start := len(text)
	for i := 0; i < lines; i++ {
		boundary := strings.LastIndexByte(text[:start], '\n')
		if boundary < 0 {
			start = -1
			break
		}
		start = boundary
	}
	if start >= 0 {
		start++
	} else {
		start = 0
	}
	rows := strings.Split(text[start:], "\n")
	for i := range rows {
		rows[i] = strings.TrimSuffix(rows[i], "\r")
	}
	return strings.Join(rows, "\n"), len(rows), nil
}
