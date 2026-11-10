param(
    [ValidateSet('auto','cuda','vulkan','cpu')][string]$Backend = 'auto',
    # Which PLaMo 3 Translate size to install. Without -Model the model named in config.json
    # is kept (2b on a fresh install); with it, config.json is switched to that model.
    [ValidateSet('0.8b','2b','8b')][string]$Model = '2b'
)
# Portable Clipboard Translator setup. The downloads are done by the binary's
# setup subcommand (Issue #152); this script only forwards the arguments to
# the ClipboardTranslator.exe next to it, so Setup.cmd, install.ps1, Scoop
# and the app's own "run setup" offer keep working unchanged.
# Windows 10/11 x64; downloads only from official PFN Hugging Face and
# official ggml-org GitHub release assets (through the binary).
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
# Only an explicit -Backend cuda/vulkan/cpu may rewrite the engine settings of an existing setup.
$explicitBackend = $PSBoundParameters.ContainsKey('Backend') -and $PSBoundParameters['Backend'] -ne 'auto'
$explicitModel = $PSBoundParameters.ContainsKey('Model')
$root = $PSScriptRoot

# The models setup can install. Each file is pinned to one Hugging Face commit and one
# SHA-256 (the model and the chat template), so a moved "main" or a corrupted download cannot
# slip through. To update a model: bump its Revision, Sha256 and TemplateSha256 together.
# MinBytes only guards files that are already on disk (they may come from an older revision).
# Only Q4_K_M is offered; the 31B model is not (about 20 GB).
# This catalog is the pinned record the checker tools read; the binary carries the
# same values in internal/models and does the work.
function Get-ModelCatalog {
    return [ordered]@{
        '0.8b' = @{ Repo = 'plamo-3-translate-0.8b-GGUF'; File = 'plamo-3-translate-0.8b-Q4_K_M.gguf'; Size = '0.6 GB'; MinBytes = 400MB
            Revision = '0c4af2b003fdd94b6868aa955414c1c86ee96997'
            Sha256 = '73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079'
            TemplateSha256 = '084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e' }
        '2b' = @{ Repo = 'plamo-3-translate-2b-GGUF'; File = 'plamo-3-translate-2b-Q4_K_M.gguf'; Size = '1.8 GB'; MinBytes = 1GB
            Revision = '228a27cc0bb4f111219963848d50a3127c16ca73'
            Sha256 = '1ffd9073afcc70e35ccc942428aa2b73d7aa2b9ea779bf504b775369e0276c13'
            TemplateSha256 = 'ad7eb87b161c6792be070d777a9f9da50c9e57a77e1fa5cca3a24b91b6bdb9e9' }
        '8b' = @{ Repo = 'plamo-3-translate-8b-GGUF'; File = 'plamo-3-translate-8b-Q4_K_M.gguf'; Size = '5.3 GB'; MinBytes = 4GB
            Revision = '407c15bc44f6968e6734bc047d92f766c163bbec'
            Sha256 = 'e021fb83dcda393a4f866291caa53380eebbe2f25be4c7585fb19e8486828bbf'
            TemplateSha256 = '084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e' }
    }
}

# --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---
function Get-LlamaRelease {
    return [ordered]@{
        Tag = 'b12032'
        Assets = [ordered]@{
            'cpu' = [ordered]@{
                File   = 'llama-b12032-bin-win-cpu-x64.zip'
                Sha256 = '5296f5d18bb7d422c7c26f4bb9bc796d9d4f41e1312a8c296cff475bdc60d0d4'
            }
            'cuda' = [ordered]@{
                File   = 'llama-b12032-bin-win-cuda-12.4-x64.zip'
                Sha256 = '36c7e24ad048cceabf00c1b49d0f0411b954c6a410a7ff4300af8721a94fd00a'
            }
            'cudart' = [ordered]@{
                File   = 'cudart-llama-bin-win-cuda-12.4-x64.zip'
                Sha256 = '8c79a9b226de4b3cacfd1f83d24f962d0773be79f1e7b75c6af4ded7e32ae1d6'
            }
            'vulkan' = [ordered]@{
                File   = 'llama-b12032-bin-win-vulkan-x64.zip'
                Sha256 = '19a8bbf269fa683ae7bc04f886c9c2a93e0655c2606f37d2b60069bf9e8bcc32'
            }
        }
    }
}
# --- end llama.cpp pinned release ---

$exe = Join-Path $root 'ClipboardTranslator.exe'
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
    throw "ClipboardTranslator.exe was not found next to setup.ps1 in $root. Setup needs the full release folder."
}
$setupArgs = @('setup', '--root', "$root")
if ($explicitBackend) { $setupArgs += @('--backend', $Backend) }
if ($explicitModel) { $setupArgs += @('--model', $Model.ToLowerInvariant()) }
$argString = ($setupArgs | ForEach-Object { '"' + ($_ -replace '"', '\"') + '"' }) -join ' '
$p = Start-Process -FilePath $exe -ArgumentList $argString -WorkingDirectory $root -Wait -PassThru -NoNewWindow
if ($p.ExitCode -ne 0) { throw "ClipboardTranslator setup failed (exit code $($p.ExitCode))." }
