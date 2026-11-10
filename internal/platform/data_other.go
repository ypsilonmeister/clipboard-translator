//go:build !darwin

package platform

func WritableAppDir(dir string) (string, error) { return dir, nil }
