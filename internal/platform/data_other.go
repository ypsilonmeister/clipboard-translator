//go:build !darwin

package platform

func writableAppDir(dir string) (string, error) { return dir, nil }
