package main

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

type ScanProgress struct {
	Visited int64  `json:"visited"`
	Errors  int64  `json:"errors"`
	Path    string `json:"path"`
}

type scanControl struct {
	ctx      context.Context
	progress ScanProgress
	notify   func(ScanProgress)
	last     time.Time
}

func (s *scanControl) check() error {
	if s == nil {
		return nil
	}
	return s.ctx.Err()
}

func (s *scanControl) walk(root string, fn filepath.WalkFunc) error {
	if err := s.check(); err != nil {
		return err
	}
	return safeWalk(root, func(path string, info os.FileInfo, err error) error {
		if stop := s.check(); stop != nil {
			return stop
		}
		if s != nil {
			s.progress.Visited++
			s.progress.Path = path
			if err != nil {
				s.progress.Errors++
			}
			if time.Since(s.last) >= 200*time.Millisecond {
				s.flush()
			}
		}
		return fn(path, info, err)
	})
}

func (s *scanControl) flush() {
	if s != nil && s.notify != nil {
		s.notify(s.progress)
		s.last = time.Now()
	}
}
