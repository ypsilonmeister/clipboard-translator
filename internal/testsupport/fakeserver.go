package testsupport

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// FakeTranslation is what the fake server answers to every chat request.
const FakeTranslation = "fake translation"

// MaybeRunFakeServer makes a test binary double as a fake llama-server. Call it
// first in TestMain: when CT_TEST_FAKE_SERVER=1 is set (tests start the binary
// themselves with that variable) it serves until killed and exits the process;
// otherwise it returns at once.
func MaybeRunFakeServer() {
	if os.Getenv("CT_TEST_FAKE_SERVER") != "1" {
		return
	}
	runFakeServer()
	os.Exit(0)
}

func runFakeServer() {
	port := "0"
	for i, a := range os.Args {
		if a == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	// A model path with FAIL in it makes the fake server exit at once, like a
	// model that cannot be loaded (too little memory, a damaged file).
	for i, a := range os.Args {
		if a == "-m" && i+1 < len(os.Args) && strings.Contains(os.Args[i+1], "FAIL") {
			os.Exit(1)
		}
	}
	// CT_TEST_FAKE_EXIT_CODE makes it exit at once with that code, like a
	// llama-server whose DLL cannot be loaded (0xC0000135 on Windows).
	if c, err := strconv.ParseUint(os.Getenv("CT_TEST_FAKE_EXIT_CODE"), 10, 32); err == nil {
		os.Exit(int(c))
	}
	// CT_TEST_FAKE_SERVER_DELAY (milliseconds) makes the fake server slow to come up,
	// like a big model loading, so a "busy" screen can be looked at.
	if ms, _ := strconv.Atoi(os.Getenv("CT_TEST_FAKE_SERVER_DELAY")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	// Like llama-server: with LLAMA_API_KEY set, every endpoint but /health
	// needs "Authorization: Bearer <key>"; without it, nothing is checked.
	key := os.Getenv("LLAMA_API_KEY")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":401,"message":"Invalid API Key","type":"authentication_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"`+FakeTranslation+`"},"finish_reason":"stop"}]}`)
	})
	_ = http.ListenAndServe("127.0.0.1:"+port, mux)
}
