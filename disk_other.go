//go:build !linux && !darwin

package main

import "errors"

func readDiskUsage(path string) (*ResourceUsage, error) {
	return nil, errors.New("disk sampling is currently supported on Linux and macOS")
}
