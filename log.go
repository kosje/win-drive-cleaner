package main

import (
	"io"
	"os"
	"strings"
)

// Read backwards in blocks rather than loading a potentially huge log.
func readLogTail(f *os.File, lines int) (string, int, error) {
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	var data []byte
	pos := info.Size()
	const maxBytes = 4 << 20
	for pos > 0 && len(data) < maxBytes {
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
		data = append(block[:nread], data...)
		if strings.Count(string(data), "\n") > lines {
			break
		}
	}
	text := string(data)
	if pos > 0 {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		} else {
			text = ""
		}
	}
	rows := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if text == "" {
		return "", 0, nil
	}
	if len(rows) > lines {
		rows = rows[len(rows)-lines:]
	}
	return strings.Join(rows, "\n"), len(rows), nil
}
