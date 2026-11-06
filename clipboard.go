package main

type Clipboard interface {
	Read() (string, error)
	Write(string) error
	// ReplaceIfUnchanged atomically compares the current clipboard text with
	// expected, and replaces it with translated only when they match.
	// Windows implementation holds OpenClipboard across compare and write.
	ReplaceIfUnchanged(expected, translated string) (bool, error)
	Close() error
}
