package setup

import "fmt"

// llamaAsset is one pinned llama.cpp release file.
type llamaAsset struct {
	File   string
	Sha256 string
}

func (a llamaAsset) url() string {
	return "https://github.com/ggml-org/llama.cpp/releases/download/" + llamaTag + "/" + a.File
}

// The pinned release below is the record the binary installs from. The
// setup scripts carry the same values (tools/bump_llama_pin.py rewrites
// both; the tests keep them in agreement). Never let a release asset
// float to "latest": a moved tag or a corrupt mirror must fail the
// SHA-256 check, not slip through.
// --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---
const llamaTag = "b12359"

var llamaAssets = map[string]llamaAsset{
	"win/cpu":      {File: "llama-b12359-bin-win-cpu-x64.zip", Sha256: "837c1655e87df3b66989f4be33a792a1399c785bf259038dc75c900096683e69"},
	"win/cuda":     {File: "llama-b12359-bin-win-cuda-12.4-x64.zip", Sha256: "e9c0abf442e8339c06728366c1ad2c711332f9939c0c00603fb75d85fd44c914"},
	"win/cudart":   {File: "cudart-llama-bin-win-cuda-12.4-x64.zip", Sha256: "8c79a9b226de4b3cacfd1f83d24f962d0773be79f1e7b75c6af4ded7e32ae1d6"},
	"win/vulkan":   {File: "llama-b12359-bin-win-vulkan-x64.zip", Sha256: "e8d18c6e4210cca87e7307c2ea78363ac8d158e4e1397358ec96ab70b53a0d5e"},
	"linux/cpu":    {File: "llama-b12359-bin-ubuntu-x64.tar.gz", Sha256: "0bc1f091e8916102b6d8f2cf944233a620e90a0f856c72d0ce41150c65c2e72e"},
	"linux/vulkan": {File: "llama-b12359-bin-ubuntu-vulkan-x64.tar.gz", Sha256: "19b60f00395a3f4954e46a5037695bc1d843404fc9d3124f82a612ecd8fbe9f0"},
	"darwin/arm64": {File: "llama-b12359-bin-macos-arm64.tar.gz", Sha256: "82b00abc740160e1b9980e6cc79c117345fa34af058c80e6c0a69aa8ddccde75"},
	"darwin/x64":   {File: "llama-b12359-bin-macos-x64.tar.gz", Sha256: "25320d529077ef5a3d16e7b5da26438f221d88c8c0930a32b0c79457f5eede56"},
}

// --- end llama.cpp pinned release ---

func llamaAssetFor(key string) (llamaAsset, error) {
	a, ok := llamaAssets[key]
	if !ok || a.File == "" || len(a.Sha256) != 64 {
		return llamaAsset{}, fmt.Errorf("the pinned llama.cpp release has no complete %s asset; reinstall or update the app", key)
	}
	return a, nil
}
