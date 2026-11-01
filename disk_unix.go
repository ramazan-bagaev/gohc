//go:build linux || darwin

package main

import (
	"errors"
	"syscall"
)

func readDiskUsage(path string) (*ResourceUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}
	blockSize := uint64(stat.Bsize)
	total := stat.Blocks * blockSize
	free := stat.Bfree * blockSize
	available := uint64(0)
	if stat.Bavail > 0 {
		available = uint64(stat.Bavail) * blockSize
	}
	if total == 0 || free > total || available > free {
		return nil, errors.New("invalid filesystem capacity")
	}
	used := total - free
	percent := float64(0)
	if used+available > 0 {
		percent = 100 * float64(used) / float64(used+available)
	}
	return &ResourceUsage{TotalBytes: total, UsedBytes: used, AvailableBytes: available, Percent: percent, Path: path}, nil
}
