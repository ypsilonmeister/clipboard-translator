<p align="center"><img src="assets/icon.png" alt="Clipboard Translator のアイコン" width="128"></p>

# clipboard-translator

[English](README.md) | 日本語

Windows、Linux（X11）、macOS 用の、日本語 <-> 英語のクリップボード翻訳ツールです。テキストを選択して
**Ctrl+Alt+E** を押し、翻訳結果を **Ctrl+V** で貼り付けます。

翻訳はすべて手元の PC で、[llama.cpp](https://github.com/ggml-org/llama.cpp) と
[PLaMo 3 Translate 2B (GGUF)](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF)
を使って行います。Ollama は不要です。Windows では Python も不要で、Linux／macOS ではセットアップ時だけ使用します。

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
  (モデルを含むインストールフォルダーごと削除されます)。
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

## macOS（実験的対応、macOS 13 以降）

CI は Apple Silicon（`arm64`）と Intel（`amd64`）用に、それぞれ
`clipboard-translator-macos-<アーキテクチャ>-<バージョン>.zip` と SHA-256 ファイルを作成します。
開発版は PR の Actions 成果物、タグ付きビルドは Releases から取得できます。
対応する zip を展開し、権限を与える前に **Clipboard Translator.app** を最終配置先（例: Applications）へ移してください。
これは **ad-hoc 署名の開発用ビルド**です。Developer ID 署名・公証は含まず、Gatekeeper の受け入れや
一般配布の署名手順は未検証です。

初回セットアップには `tarfile.data_filter` が使える Python（3.12 以降、またはセキュリティ更新済みの
3.10／3.11）が必要です。例えば Homebrew で Python を導入してください。
アプリを開き、ダウンロードの確認に同意すると、キャンセル可能なセットアップウィンドウを表示します。
詳細は `setup.log` に記録します。公式 llama.cpp アーカイブは GitHub 公開の SHA-256 と照合し、
標準で Metal を使用します。同じランタイムを CPU としても利用でき、メニューから切り替えられます。
モデルは Windows／Linux と同様にサイズで不完全な取得を検出し、固定ハッシュとは照合しません。

アプリは Dock ではなく **メニューバー**に常駐します。標準のキーは **Control+Option+E**
（設定上は `Ctrl+Alt+E`）、貼り付けは **Command+V** です。
選択テキストの取得（Cmd+C の送信）と任意の自動貼り付け（Cmd+V）には、
**システム設定 > プライバシーとセキュリティ > アクセシビリティ**でアプリへの許可が必要です。
メニューの **Open Accessibility Settings...** から設定ページを開けます。権限の要求は自動では行いません。
許可がない場合も、手動で Cmd+C → ホットキー → Cmd+V と操作してクリップボードを翻訳できます。
自動貼り付けは初期オフで、修飾キーを離した後にも最前面のプロセスとウィンドウを確認します。
同じウィンドウ内の選択変更は確実に検出できず、クリップボードの比較・置換も不可分な操作ではありません。
アプリのサンドボックスや Secure Input により操作できない場合があります。

設定・モデル・ログはアプリ本体を変更せず、次の場所に保存します。

```text
~/Library/Application Support/ClipboardTranslator/
    config.json, setup.sh, setup.log, translator.log, server.log
    models/, runtime/
```

埋め込みの macOS 用スクリプトはここに `setup.sh` として保存します。
手動で CPU に切り替える場合はアプリを終了し、
`bash "$HOME/Library/Application Support/ClipboardTranslator/setup.sh" --backend cpu`
を実行します（Metal は `metal`）。**Start at login** は
`~/Library/LaunchAgents/com.github.ypsilonmeister.clipboard-translator.plist` だけを管理し、
次回ログインから適用します。アプリを移動した場合は設定を入れ直してください。
更新は終了してアプリを置き換える方式で、Application Support のデータは残ります。
状態やエラーの通知はメニューバーのツールチップとログに表示し、通知センターの権限は要求しません。
操作が必要なエラーにはネイティブダイアログも使用します。

Mac または GitHub の macOS ランナーで Go と Xcode Command Line Tools を用意し、
`bash tools/package-macos.sh dev` を実行すると、その環境のアーキテクチャ向けにビルドします。
`.app` の情報・アイコン・ad-hoc 署名と検証を行い、`dist/` に zip とチェックサムを作成します。
CI は Apple Silicon／Intel の両方で、AppKit、クリップボード、Carbon ホットキーの登録・イベント配送、
メニューを検証します。キー送信やアクセシビリティ権限の付与は行いません。
他アプリでの選択取得・貼り付け、US 以外のキーボード、Gatekeeper、Metal の性能は実機での確認が必要です。
`CGO_ENABLED=0` のクロスコンパイルではロジックとスタブのみを確認できます。

## Linux（X11、実験的対応）

[#13](https://github.com/ypsilonmeister/clipboard-translator/issues/13) の最初の段階として Linux に対応しています。
**Wayland セッション（XWayland を含む）は非対応**です。
ログイン時に Xorg／X11 セッションを選んでください。

Ubuntu 24.04 では、実行に必要なパッケージを入れます。

```bash
sudo apt-get install libgtk-3-0t64 libxtst6 python3 xdg-utils libnotify-bin
```

Releases の Linux 用 `tar.gz` をホーム以下の書き込み可能な専用フォルダーに展開し、実行します。

```bash
cd clipboard-translator-linux-amd64-<version>
bash setup.sh                  # モデルと公式 CPU ランタイムを取得
./clipboard-translator
# Vulkan ドライバーが使える環境で、明示的に追加する場合:
bash setup.sh --backend vulkan
```

セットアップには `tarfile.data_filter` が使える Python（3.12 以降、またはセキュリティ更新済みの
3.10／3.11）が必要です。ランタイムのアーカイブは GitHub 公開の SHA-256 と照合します。
標準は CPU で、Vulkan が起動できなければ導入済みの CPU に切り替わります。
モデルは Windows と同様にサイズで不完全な取得を検出し、固定ハッシュとの照合は行いません。
ランタイムの共有ライブラリとリンクはアーカイブの配置を保って導入します。
配布元ランタイムのシステム要件も適用されます。古いディストリビューションでは、対応する
llama.cpp を自身でビルドし、`runtime/cpu/llama-server` に配置する必要があります。

Linux では Ctrl+C を送らず、X11 の **PRIMARY** 選択から直接読み取り、翻訳前に CLIPBOARD に入れます。
アプリによっては PRIMARY に最後の選択が残ります。PRIMARY に文字がないときは既存の CLIPBOARD を使います。
X11 にクリップボードの不可分な比較・置換機能はないため、書き込み直前に文字を比較しますが、
Windows のように他アプリとの競合を完全には防げません。クリップボードマネージャーがない場合、
終了時にクリップボードの内容が失われることがあります。

トレイとメニューは GTK 3 で表示します。互換性のあるトレイ領域がない環境では、小さな操作ウィンドウを
表示し、メニューや Quit を使えるようにします。通知には任意で `notify-send` を使用します。
自動貼り付けは初期オフで、XTest による Ctrl+V を使用し、修飾キーを離した後にもフォーカスを確認します。
同じウィンドウ内での選択変更は確実に検出できないため、必要に応じて手動で貼り付けてください。
**Start at login** は `~/.config/autostart/clipboard-translator.desktop` のみを管理します
（`XDG_CONFIG_HOME` があればその下）。移動後は一度オフにしてからオンにし直してください。
メニューの Quit、SIGTERM、実行ファイル隣の `STOP` ファイルで終了できます。
Linux の更新は、終了して実行ファイルとセットアップスクリプトを置き換え、`config.json`、`models`、
`runtime` を残す方法です。PowerShell インストーラーと Scoop は Windows 専用です。

Ubuntu 24.04 上でソースからビルドする場合（Go 1.21 以降と C コンパイラーが必要）:

```bash
sudo apt-get install build-essential pkg-config libgtk-3-dev libx11-dev libxtst-dev
bash tools/package-linux.sh dev
# dist/clipboard-translator-linux-amd64-dev.tar.gz を書き込み可能なフォルダーへ展開します。
```

`CGO_ENABLED=0` のビルドはロジックのテスト用で、Linux のデスクトップ機能は使えません。
CI では隔離した Xvfb 環境で入力、PRIMARY／CLIPBOARD、貼り付け、メニューを検証します。
翻訳品質、実際のデスクトップでのトレイ表示、GPU ドライバーは実機での確認が必要です。

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

## ビルド（Windows）

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
  `go vet`、`go test`、リリース用のフラグでのビルドを行い、PowerShell の構文も確認します。
  Linux では Xvfb 上の X11 デスクトップ機能、セットアップのフィクスチャテスト、tarball 作成も検証します。
  macOS は Apple Silicon／Intel の両方でネイティブテストとアプリの作成を行います。
- `v0.6.0` のようなタグを push すると、`.github/workflows/release.yml` が動きます。タグをバージョンとして
  exe をビルドし、`Portable-Clipboard-Translator-Windows-x64-<タグ>.zip` にまとめて、GitHub のリリースに
  添付します。Ubuntu 24.04 で作成した Linux amd64 用 tarball と SHA-256 ファイルも添付します。
  macOS Apple Silicon／Intel 用の ad-hoc 署名済みアプリ zip と SHA-256 ファイルも添付します。
  そのタグのリリースがすでにあれば、成果物を追加します。
- `bucket/clipboard-translator.json` は Scoop のマニフェストです。リリースのたびに更新します
  (`scoop checkver -u` でできます)。

不具合の報告や機能の要望には、Issue フォームを使ってください。アプリのバージョン、インストール方法、
ログの抜粋を尋ねます。

## ライセンス

このリポジトリは [MIT License](LICENSE) です。

モデルと llama.cpp は同梱していません。`setup.ps1`／`setup.sh` が公式の配布元からダウンロードします。
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
