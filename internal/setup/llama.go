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
const llamaTag = "b12032"

var llamaAssets = map[string]llamaAsset{
	"win/cpu":      {File: "llama-b12032-bin-win-cpu-x64.zip", Sha256: "5296f5d18bb7d422c7c26f4bb9bc796d9d4f41e1312a8c296cff475bdc60d0d4"},
	"win/cuda":     {File: "llama-b12032-bin-win-cuda-12.4-x64.zip", Sha256: "36c7e24ad048cceabf00c1b49d0f0411b954c6a410a7ff4300af8721a94fd00a"},
	"win/cudart":   {File: "cudart-llama-bin-win-cuda-12.4-x64.zip", Sha256: "8c79a9b226de4b3cacfd1f83d24f962d0773be79f1e7b75c6af4ded7e32ae1d6"},
	"win/vulkan":   {File: "llama-b12032-bin-win-vulkan-x64.zip", Sha256: "19a8bbf269fa683ae7bc04f886c9c2a93e0655c2606f37d2b60069bf9e8bcc32"},
	"linux/cpu":    {File: "llama-b12032-bin-ubuntu-x64.tar.gz", Sha256: "9f10432ca3f66ef003a7a540aaffb9da0acadaefd05404521b9ab7a2436af304"},
	"linux/vulkan": {File: "llama-b12032-bin-ubuntu-vulkan-x64.tar.gz", Sha256: "f75e7107f9450034c5467492c41e9ed91816e2d815fc4b802c6bd58de7d5b126"},
	"darwin/arm64": {File: "llama-b12032-bin-macos-arm64.tar.gz", Sha256: "00468fe833eb000b78a748122f5921b6c7b07dc82331a8d0c68196c69cb26382"},
	"darwin/x64":   {File: "llama-b12032-bin-macos-x64.tar.gz", Sha256: "291d99f3b6f45f67f73afe5381540f29928840a1a14d9c19d6595bd649a035fc"},
}

// --- end llama.cpp pinned release ---

func llamaAssetFor(key string) (llamaAsset, error) {
	a, ok := llamaAssets[key]
	if !ok || a.File == "" || len(a.Sha256) != 64 {
		return llamaAsset{}, fmt.Errorf("the pinned llama.cpp release has no complete %s asset; reinstall or update the app", key)
	}
	return a, nil
}
