//go:build !windows

package app

import "os"

func truncateSparse(f *os.File, size int64) error { return f.Truncate(size) }
