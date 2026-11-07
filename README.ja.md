<p align="center"><img src="assets/icon.png" alt="Clipboard Translator のアイコン" width="128"></p>

# clipboard-translator

[English](README.md) | 日本語

Windows 用の、日本語 <-> 英語のクリップボード翻訳ツールです。テキストを選択して
**Ctrl+Alt+E** を押し、翻訳結果を **Ctrl+V** で貼り付けます。

翻訳はすべて手元の PC で、[llama.cpp](https://github.com/ggml-org/llama.cpp) と
[PLaMo 3 Translate 2B (GGUF)](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF)
を使って行います。Ollama も Python も要りません。

**Built with PLaMo.** 翻訳モデルは Preferred Networks 社の PLaMo で、
[PLaMo コミュニティライセンス](https://plamo.preferredai.jp/info/plamo-community-license-ja)
のもとで利用しています。業務で使う場合は、先に[ライセンス](#ライセンス)の項を読んでください。

> **状態: プロトタイプ。** 幅広い PC や GPU ドライバーでの確認はできていません。
> 自己責任でお使いください。

## インストール(Windows 10/11 x64)

### インストールスクリプト(おすすめ)

PowerShell を開き(管理者権限は不要です)、次を実行します。

```powershell
irm https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/main/install.ps1 | iex
```

スクリプトは、最新のリリースをダウンロードして SHA-256 を確認し、
`%LOCALAPPDATA%\ClipboardTranslator` に入れます。続けて PLaMo のモデル(1 GB 超)と llama.cpp
(CUDA / Vulkan / CPU は自動で選びます。CPU 版は予備として必ず入ります)をダウンロードし、
スタートメニューのショートカットと「設定 > アプリ」の項目を作って、アプリを起動します。
短いスクリプトなので、実行前に [`install.ps1`](install.ps1) を読んでもかまいません。

- **更新:** 同じコマンドをもう一度実行するか、トレイメニューの「Check for updates」を使います。
  `config.json`、モデル、llama.cpp はそのまま残ります。
- **アンインストール:** 「設定 > アプリ」の *Clipboard Translator* から、または
  `& ([scriptblock]::Create((irm https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/main/install.ps1))) -Uninstall`
  (アプリ自身のファイル(モデルと llama.cpp を含む)を削除し、ほかに何も残っていなければフォルダーも
  削除します)。インストールは空のフォルダーか新しいフォルダーにだけ行うので、他のファイルと混ざることはありません。
- **オプション**(`| iex` には引数を渡せないので環境変数で指定します):
  `CT_VERSION=v0.5.0` でリリースを固定、`CT_INSTALL_DIR=D:\Apps\CT` でフォルダーを指定、
  `CT_SKIP_SETUP=1` でモデルのダウンロードを省略、`CT_NO_LAUNCH=1` で起動しない。
  例: `$env:CT_VERSION='v0.5.0'; irm ... | iex`。スクリプトを保存して実行する場合は、同じ指定を
  引数(`-Version`、`-InstallDir`、`-SkipSetup`、`-NoLaunch`、`-Force`)でも行えます。

### Scoop

```powershell
scoop bucket add clipboard-translator https://github.com/ypsilonmeister/clipboard-translator
scoop install clipboard-translator
```

`models`、`runtime`、`config.json` は `scoop update` でも残ります。この方法は新しく、
インストールスクリプトほどは試されていません。

### ポータブル zip(手動)

[Releases](https://github.com/ypsilonmeister/clipboard-translator/releases) から
`Portable-Clipboard-Translator-Windows-x64-<バージョン>.zip` をダウンロードし、
書き込み可能なフォルダーに展開します。`ClipboardTranslator.exe`(または `Start.cmd`)を実行すると、
モデルや llama.cpp が無ければダウンロードを提案します。`Setup.cmd` なら最初にまとめてダウンロードします。
`Stop.cmd` はアプリと、その子プロセスの `llama-server` を止めます。「Start with Windows」は
exe のパスを `HKCU\...\Run` に登録します。フォルダーを移動したら、一度オフにしてからオンにし直してください。

バックエンドを手動で選ぶ場合:

```powershell
powershell -ExecutionPolicy Bypass -File .\setup.ps1 -Backend vulkan   # または cuda / cpu
```

## 使い方

アプリは**システムトレイ**(通知領域)に常駐します。Windows 11 では `^` の中に隠れていることが
あります。モデルの読み込みが終わると「Ready」の吹き出しが出ます。ツールチップには状態と、
読み込み中・翻訳中の経過秒数が出ます。

日本語または英語のテキストを選択して `Ctrl+Alt+E` を押します。翻訳が終わると、結果が
クリップボードに入るので、`Ctrl+V` で貼り付けます。何も選択していないときは、
クリップボードにあるテキストを翻訳します。

トレイアイコンを右クリックすると、次のメニューが出ます(表示はすべて英語です)。

| 項目 | 内容 |
| --- | --- |
| **Cancel translation** | 翻訳中だけ表示されます。 |
| **Recent translations** | 直近 10 件。メモリ上だけに保持し、ディスクには書きません。クリックすると、その翻訳をもう一度クリップボードにコピーします。*Clear history* で空にします。 |
| **Paste automatically after translating** | 初期はオフです。オンにすると Ctrl+V を自動で送ります。ただし、選択したテキストを翻訳した場合で、かつ同じウィンドウが手前にあるときだけです。それ以外はクリップボードに入れて待ちます。 |
| **Hotkey** | 別のキーの組み合わせを選びます。他のプログラムが使っている場合は、元のキーのままです。 |
| **Translation engine** | インストール済みの llama.cpp(CPU、CUDA、Vulkan)を切り替えます。2 種類以上あるときだけ表示されます。 |
| **Check for updates** | クリックしたときだけ GitHub に最新リリースを問い合わせます。アプリが勝手に確認することはありません。 |
| **Open config.json** | そのほかの設定は[設定](#設定)を見てください。 |
| **Built with PLaMo (license)** | PLaMo コミュニティライセンスを開きます。 |
| **Start with Windows** | オン/オフの切り替え。 |
| **Open log** / **Quit** | |

翻訳サーバー(`llama-server`)が予期せず止まったときは、自動で再起動します(1 秒、2 秒、4 秒...と間隔を空けます)。
10 分以内に 3 回再起動しても直らなければ、メッセージを出して終了します。起動時のエラーはメッセージボックスに出て、
`translator.log` にも書かれます。

## プライバシーとネットワーク

- 翻訳は手元の PC で行います。サーバーは `127.0.0.1` だけで待ち受けます。
- アプリがネットワークに接続するのは、操作したときだけです。モデルと llama.cpp のダウンロード
  (`Setup.cmd`、`setup.ps1`、初回起動時の提案、インストールスクリプト)と、「Check for updates」「Update」です。
  テレメトリーも、バックグラウンドでの確認もありません。
- ログにはエラーを記録しますが、翻訳したテキストは記録しません。翻訳履歴はメモリ上だけにあり、
  アプリを終了すると消えます。

## 設定

`config.json`(パスはすべてアプリのフォルダーからの相対パスです)。トレイメニューから変えられるのは
`hotkey`、`server_file`、`gpu_layers`、`auto_paste` です。そのほかは手で編集し、再起動が必要です。
変更前のファイルは `config.json.bak` として残ります。

| キー | 内容 |
| --- | --- |
| `hotkey` | 修飾キー + A〜Z の 1 文字。例: `Ctrl+Alt+E` |
| `auto_paste` | `true` で翻訳後に Ctrl+V を自動入力(初期値は `false`) |
| `port` | ローカルの llama-server のポート(アプリは `port + 1` も多重起動の防止に使います) |
| `max_chars` | これより長いテキストは翻訳しません |
| `gpu_layers` | GPU に載せる層の数(`0` は CPU のみ) |
| `server_file` / `fallback_server_file` | llama-server の実行ファイル(通常用 / CPU の予備) |

> `setup.ps1` は、検出したバックエンドに合わせて `server_file`、`fallback_server_file`、`gpu_layers` を
> 書き換えます。そのため、セットアップ後に git 上でこのファイルが変更扱いになります。

## 制限事項

- アプリによっては、選択範囲を読むための擬似的な Ctrl+C を受け付けません。
- 選択範囲を読むとクリップボードが変わります。翻訳結果は、クリップボードのテキストが取得時のまま
  のときだけ、それを置き換えます。
- 対応するのは日本語 <-> 英語だけです。URL は翻訳しません。
- GPU のバックエンドは失敗することがあります(VRAM 不足、古いドライバー、DLL の不足など)。その場合は
  CPU 版に切り替わります。詳しくは `server.log` と `translator.log` を見てください。
- exe はコード署名していないため、ブラウザーでダウンロードした zip では、SmartScreen の警告が出ることが
  あります(インストールスクリプトで取得したファイルには出ません)。

## ビルド

[Go](https://go.dev/) 1.21 以降が必要です。

```powershell
go test ./...

# リリース用: コンソールを出さず、トレイアイコンだけで動きます
go build -ldflags "-H=windowsgui -s -w" -o ClipboardTranslator.exe .

# デバッグ用: コンソールを残して、ログをそこに出します
go build -o ClipboardTranslator.exe .
```

アイコン、エクスプローラーに表示されるバージョン情報、アプリケーションマニフェスト(モニターごとの DPI 対応。
拡大表示でもトレイのアイコンとメニューがぼやけません)は、`resource_windows_amd64.syso` からリンクされます。
Go のツールチェーンが自動で取り込み、標準ライブラリだけで生成できます。

```powershell
go run ./tools/mkicon                    # assets/icon-source.jpg -> assets/icon.ico, assets/icon.png
go run ./tools/mksyso -version 0.5.1     # アイコン + assets/app.manifest + バージョン -> resource_windows_amd64.syso
```

コミット済みの `.syso` には、直近のリリースのバージョンが入っています。リリースのワークフローが、タグから
作り直し、`main.version` も設定します。「Check for updates」はこれを最新のリリースと比べます(手元のビルドは
「development build」と表示され、古いと判定されることはありません)。`setup.ps1` は exe に埋め込まれています。

## CI とリリース

- `.github/workflows/ci.yml` は、`main` への push と、すべての Pull Request で、Windows 上の `gofmt`、
  `go vet`、`go test`、リリース用のフラグでのビルドを行います。あわせて、Windows 以外のスタブのコンパイルと、
  PowerShell スクリプトの構文も確認します。
- `v0.6.0` のようなタグを push すると、`.github/workflows/release.yml` が動きます。タグをバージョンとして
  exe をビルドし、`Portable-Clipboard-Translator-Windows-x64-<タグ>.zip` にまとめて、GitHub のリリースに
  添付します(そのタグのリリースがすでにあれば、zip だけを追加します)。
- `bucket/clipboard-translator.json` は Scoop のマニフェストです。リリースのたびに更新します
  (`scoop checkver -u` でできます)。

不具合の報告や機能の要望には、Issue フォームを使ってください。アプリのバージョン、インストール方法、
ログの抜粋を尋ねます。

## ライセンス

このリポジトリは [MIT License](LICENSE) です。

モデルと llama.cpp は同梱していません。`setup.ps1` が公式の配布元からダウンロードします。
それぞれ別のライセンスです。

**PLaMo。** モデルは Preferred Networks 社の
[PLaMo コミュニティライセンス](https://plamo.preferredai.jp/info/plamo-community-license-ja)
のもとで利用しています(**Built with PLaMo**)。この README を書いた時点の条文から要約すると
(条文は変更されることがあります)、事業や営利のために使う場合(組織内部での利用を含みます)は、
事前に提供元の登録ページに登録し、かつ、ご自身(と関係会社)の直近事業年度の収入または売上が 10 億円を
超えなければ、無償で利用できます。モデルやその出力を第三者に提供する人は、「Built with PLaMo」の表示と
ライセンスのコピーの提供が必要で、モデルが生成したテキストを公表するときは、そのことを明示する必要があります。
必ずライセンス本文を読んでください。この要約は法的な助言ではありません。ライセンスには寄付についての
記載がなく、受け取ることが商業利用にあたるかどうかは、提供元に確認すべき事項です。

[llama.cpp](https://github.com/ggml-org/llama.cpp) は MIT ライセンスです。
