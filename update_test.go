package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.5.1", "0.5.0", 1},
		{"v0.5.1", "0.5.1", 0},
		{"0.10.0", "0.9.0", 1}, // numeric, not string, order
		{"0.9.0", "0.10.0", -1},
		{"1.0", "1.0.0", 0}, // missing parts count as 0
		{"1.0.1", "1.0", 1},
		{"0.6.0-rc1", "0.5.9", 1},
		{"dev", "0.5.0", 0}, // not comparable
		{"0.5.0", "dev", 0},
		{"", "0.5.0", 0},
		{"abc", "def", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if isNewerVersion("v0.5.1", "dev") {
		t.Error("a development build must never be told it is outdated")
	}
	if !isNewerVersion("v0.6.0", "0.5.1") || isNewerVersion("v0.5.1", "0.5.1") {
		t.Error("isNewerVersion is wrong")
	}
}

func TestDetectInstallKind(t *testing.T) {
	cases := []struct {
		dir, rec string
		want     installKind
	}{
		{`C:\Users\a\AppData\Local\ClipboardTranslator`, `C:\Users\a\AppData\Local\ClipboardTranslator`, kindInstaller},
		{`c:\users\a\appdata\local\clipboardtranslator`, `C:\Users\a\AppData\Local\ClipboardTranslator\`, kindInstaller},
		{`D:\tools\Clipboard-Translator`, `C:\Users\a\AppData\Local\ClipboardTranslator`, kindPortable},
		{`D:\tools\Clipboard-Translator`, ``, kindPortable},
		{`C:\Users\a\scoop\apps\clipboard-translator\current`, ``, kindScoop},
		{`C:\Users\a\scoop\apps\clipboard-translator\0.5.1`, `C:\Users\a\scoop\apps\clipboard-translator\0.5.1`, kindScoop},
	}
	for _, c := range cases {
		if got := detectInstallKind(c.dir, c.rec); got != c.want {
			t.Errorf("detectInstallKind(%q, %q) = %v, want %v", c.dir, c.rec, got, c.want)
		}
	}
}

func TestFetchLatest(t *testing.T) {
	var gotUA, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(`{"tag_name":"v0.6.0","html_url":"https://example.test/r/v0.6.0","extra":1}`))
		case "/notag":
			w.Write([]byte(`{"html_url":"x"}`))
		case "/bad":
			w.Write([]byte(`not json`))
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := fetchLatest(ctx, srv.URL+"/ok")
	if err != nil || info.Tag != "v0.6.0" || info.URL != "https://example.test/r/v0.6.0" {
		t.Fatalf("got %+v, %v", info, err)
	}
	if gotUA == "" || gotAccept == "" {
		t.Errorf("GitHub requires a User-Agent; got UA=%q Accept=%q", gotUA, gotAccept)
	}
	for _, p := range []string{"/notag", "/bad", "/missing"} {
		if _, err := fetchLatest(ctx, srv.URL+p); err == nil {
			t.Errorf("%s: expected an error", p)
		}
	}
}
