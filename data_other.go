//go:build !darwin

package main

func writableAppDir(dir string) (string, error) { return dir, nil }
