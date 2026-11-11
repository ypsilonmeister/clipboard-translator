<p align="center"><img src="assets/icon.png" alt="Clipboard Translator のアイコン" width="128"></p>

# clipboard-translator

[English](README.md) | 日本語

Windows 用のローカルクリップボード翻訳ツールです（macOS／Linux 版は実験的対応）。
どのアプリでも、テキストを選択して **Ctrl+Alt+E** を押すと、翻訳がクリップボードに入り、
**Ctrl+V** でそのまま貼り付けられます。53 言語に対応し、既定は日本語 <-> 英語です。

翻訳はすべて手元の PC で、[llama.cpp](https://github.com/ggml-org/llama.cpp) と
Preferred Networks の [PLaMo 3 Translate (GGUF)](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF)
を使って行います。テキストが外部に出ることはありません。クラウドも、アカウントも、
API キーも、文字数の制限もありません。オンライン翻訳には貼りたくない文章、
たとえば仕事のメールやコードのコメント、社内文書にも使えます。

モデルの大きさは設定画面で選べます（0.8B／2B／8B）。ほかに用意するものはありません。
Ollama も Python も不要で、初回セットアップはアプリ本体が担います。

**Built with PLaMo.** 翻訳モデルは Preferred Networks 社の PLaMo で、
[PLaMo コミュニティライセンス](https://plamo.preferredai.jp/info/plamo-community-license-ja)
のもとで利用しています。業務で使う場合は、先に[ライセンス](#ライセンス)の項を読んでください。

> **状態: プロトタイプ。** 幅広い PC や GPU ドライバーでの確認はできていません。
> 自己責任でお使いください。

## どれをダウンロードするか

[リリース](https://github.com/ypsilonmeister/clipboard-translator/releases)には、環境ごとに 1 つずつファイルがあります
(macOS と Linux のファイルには隣に `.sha256` ファイルが付きます。Windows の zip の SHA-256 はリリースノートに載ります)。

| お使いのコンピューター | ファイル | 手順 |
| --- | --- | --- |
| Windows 10/11 x64 | インストールスクリプト、Scoop、または `Portable-Clipboard-Translator-Windows-x64-<version>.zip` | [Windows](#インストールwindows-1011-x64) |
| Apple シリコンの Mac(M1 以降) | `clipboard-translator-macos-arm64-<version>.zip` | [macOS](#macos実験的対応macos-13-以降) |
| Intel プロセッサの Mac | `clipboard-translator-macos-amd64-<version>.zip` | [macOS](#macos実験的対応macos-13-以降) |
| Linux x86_64(X11 セッション) | `clipboard-translator-linux-amd64-<version>.tar.gz` | [Linux](#linuxx11実験的対応) |

macOS と Linux は実験的対応です。Linux arm64 用のビルドは配布していません。

## インストール(Windows 10/11 x64)

llama.cpp のビルドには **Visual C++ 再頒布可能パッケージ(x64)** が必要です。ほとんどの PC には
すでに入っています。入っていない PC(または非常に古い PC)では、`llama-server` が何も出力せず
終了コード `0xC0000135` ですぐに終了します。その場合、アプリが対処方法を表示します。
`winget install --id Microsoft.VCRedist.2015+.x64 -e` か
<https://aka.ms/vs/17/release/vc_redist.x64.exe> で入れてください。

### インストールスクリプト(おすすめ)

PowerShell を開き(管理者権限は不要です)、次を実行します。

```powershell
irm https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/main/install.ps1 | iex
```

スクリプトは、最新のリリースをダウンロードして SHA-256 を確認し、
`%LOCALAPPDATA%\ClipboardTranslator` に入れます。続けて PLaMo のモデル(1 GB 超)と固定バージョンの llama.cpp
(ピン留めされた SHA-256 と照合; CUDA / Vulkan / CPU は自動で選びます。CPU 版は予備として必ず入ります)をダウンロードし、
スタートメニューのショートカットと「設定 > アプリ」の項目を作って、アプリを起動します。
短いスクリプトなので、実行前に [`install.ps1`](install.ps1) を読んでもかまいません。

- **更新:** 同じコマンドをもう一度実行するか、トレイメニューの「Check for updates」を使います。
  トレイの「Update」は、インストールするリリースのインストーラースクリプトを実行します。
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

### モデルのサイズを選ぶ

セットアップは、初期値で PLaMo 3 Translate **2B**(1.8 GB)を入れます。別のサイズを使うには:

```powershell
powershell -ExecutionPolicy Bypass -File .\setup.ps1 -Model 8b   # または 0.8b / 2b
```

| `-Model` | ファイル | ダウンロード |
| --- | --- | --- |
| `0.8b` | `plamo-3-translate-0.8b-Q4_K_M.gguf` | 0.6 GB |
| `2b`(初期値) | `plamo-3-translate-2b-Q4_K_M.gguf` | 1.8 GB |
| `8b` | `plamo-3-translate-8b-Q4_K_M.gguf` | 5.3 GB |

モデルは設定ページ(下記)でも選んでダウンロードできます。大きいモデルほど、ディスク容量、メモリ、1回あたりの翻訳時間が必要です。小さいモデルは速くなります。
31B は選べません。`-Model` を付けると、ファイルをダウンロードし(固定した SHA-256 で検証)、`config.json` の
`model_file` を書き換えます。以前のモデルのファイルは、削除するまで `models\` に残ります。`-Model` を付けない
セットアップ(Scoop の更新を含む)は、`config.json` に書かれているモデルのままにします。どのサイズも同じ
[PLaMo コミュニティライセンス](https://plamo.preferredai.jp/info/plamo-community-license-ja)です。
Linux と macOS では `--model 0.8b|2b|8b` を使います(例: `bash setup.sh --backend cpu --model 8b`)。

## macOS（実験的対応、macOS 13 以降）

### インストール手順

1. **お使いの Mac に合うファイルを選びます。** アップルメニュー > *この Mac について* のチップが
   *Apple M…* なら `arm64`、*Intel* なら `amd64` です。ターミナルで `uname -m` を実行しても分かります
   (`arm64` または `x86_64`)。
2. Releases から `clipboard-translator-macos-<アーキテクチャ>-<バージョン>.zip` と `.sha256` ファイルを
   **ダウンロード**し、2 つのファイルがあるフォルダーで内容を確認します。

   ```bash
   shasum -a 256 -c clipboard-translator-macos-<アーキテクチャ>-<バージョン>.zip.sha256
   ```

3. zip を**展開**し、**Clipboard Translator.app** を `/Applications`(または置いておきたい場所)へ移します。
4. **初回の起動。** このアプリは公証されていないため、macOS が開くのを拒否することがあります
   (表示や手順は macOS のバージョンで異なります)。最初に開こうとして拒否されたあと、
   *システム設定 > プライバシーとセキュリティ* で **このまま開く** を選ぶか、開く前に
   ダウンロード時の隔離属性を外してください。

   ```bash
   xattr -dr com.apple.quarantine "/Applications/Clipboard Translator.app"
   ```

5. **アプリを起動**し、ダウンロードの確認(モデルは 1 GB 超)に同意します。そのあと、選択範囲をコピーできるように
   *システム設定 > プライバシーとセキュリティ > アクセシビリティ* で許可します。

以下に、それぞれの詳細を説明します。

CI は Apple Silicon（`arm64`）と Intel（`amd64`）用に、それぞれ
`clipboard-translator-macos-<アーキテクチャ>-<バージョン>.zip` と SHA-256 ファイルを作成します。
開発版は PR の Actions 成果物、タグ付きビルドは Releases から取得できます。
対応する zip を展開し、権限を与える前に **Clipboard Translator.app** を最終配置先（例: Applications）へ移してください。
これは **ad-hoc 署名の開発用ビルド**です。Developer ID 署名・公証は含まず、Gatekeeper の受け入れや
一般配布の署名手順は未検証です。
開発用ビルドの置き換え後は、アクセシビリティ権限の再設定が必要になる場合があります。

初回セットアップはアプリ本体（`setup` サブコマンド）が担うため、Python は不要です。
アプリを開き、ダウンロードの確認に同意すると、キャンセル可能なセットアップウィンドウを表示します。
詳細は `setup.log` に記録します。固定された公式 llama.cpp アーカイブをピン留めされた SHA-256 と照合し、
標準で Metal を使用します。同じランタイムを CPU としても利用でき、フォールバックとして利用できるほか、設定画面（Translation engine カード）で切り替えられます。
モデルとチャットテンプレートは、Windows／Linux と同様に、新しくダウンロードしたものを固定した SHA-256 と照合します(サイズの確認は、すでにディスクにあるファイルを守るためだけです)。

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

## Linux（X11／Wayland、実験的対応）

### インストール手順

1. `echo "$XDG_SESSION_TYPE"` でセッションを確認します。X11（Xorg）、または下の
   「Wayland での操作」に記載した portal を備える Wayland デスクトップを使います。
2. 依存パッケージ(下記の Ubuntu 24.04 の例)を入れたあと、Releases から
   `clipboard-translator-linux-amd64-<version>.tar.gz` と `.sha256` ファイルをダウンロードします(x86_64 のみ)。
   内容を確認し、ホーム以下の専用フォルダーに展開して実行します。

   ```bash
   sha256sum -c clipboard-translator-linux-amd64-<version>.tar.gz.sha256
   tar -xzf clipboard-translator-linux-amd64-<version>.tar.gz -C ~
   cd ~/clipboard-translator-linux-amd64-<version>
   bash setup.sh            # モデルと公式 CPU ランタイムを取得
   ./clipboard-translator
   ```

以下に、それぞれの詳細を説明します。

[#13](https://github.com/ypsilonmeister/clipboard-translator/issues/13) の最初の段階として Linux に対応しています。
Wayland は実験的対応で、最初の対象は GNOME 48+ と、対応する最新の
GlobalShortcuts・RemoteDesktop キーボード・Clipboard portal です。
ショートカットとキーボード・クリップボード操作の許可画面を許可し、ホットキーを
押した後は修飾キーを離してください。翻訳結果はクリップボードに入ります。
**Wayland では自動貼り付けを無効にしています**。
実際のショートカットはデスクトップ側が決定し、Settings と Quick start に表示します。
許可を拒否・取り消した場合は、操作ウィンドウのメニューから
**Retry Wayland permissions** を選んでください。トレイがない環境では操作ウィンドウを
開いたまま使います（閉じるとアプリが終了します）。必要な portal がない古いデスクトップ
では Xorg／X11 セッションが必要です。KDE と wlroots は未検証で、実機確認済みなのは
GNOME 50.1 のテスト VM です。

Ubuntu 24.04 では、実行に必要なパッケージを入れます。

```bash
sudo apt-get install libgtk-3-0t64 libxtst6 xdg-utils libnotify-bin
```

Releases の Linux 用 `tar.gz` をホーム以下の書き込み可能な専用フォルダーに展開し、実行します。

```bash
cd clipboard-translator-linux-amd64-<version>
bash setup.sh                  # モデルと公式 CPU ランタイムを取得
./clipboard-translator
# Vulkan ドライバーが使える環境で、明示的に追加する場合:
bash setup.sh --backend vulkan
```

セットアップはアプリ本体（`setup.sh` はその `setup` サブコマンドへ転送します）が担うため、
Python は不要です。ランタイムのアーカイブはピン留めされた SHA-256 と照合します。
標準は CPU で、Vulkan が起動できなければ導入済みの CPU に切り替わります。
モデルとチャットテンプレートは、Windows と同様に、新しくダウンロードしたものを固定した SHA-256 と照合します(サイズの確認は、すでにディスクにあるファイルを守るためだけです)。
ランタイムの共有ライブラリとリンクはアーカイブの配置を保って導入します。
配布元ランタイムのシステム要件も適用されます。古いディストリビューションでは、対応する
llama.cpp を自身でビルドし、`runtime/cpu/llama-server` に配置する必要があります。

### X11 での操作

X11 では Ctrl+C を送らず、**PRIMARY** 選択から直接読み取り、翻訳前に CLIPBOARD に入れます。
ホットキーを押したウィンドウにフォーカスがあるときに作られた選択だけを自動貼り付けの対象にします。PRIMARY が
別ウィンドウに残っている場合や判別できない場合は、その文字を翻訳しますが自動貼り付けはしません。PRIMARY に文字がないときは、
既存の CLIPBOARD を使います。
X11 にクリップボードの不可分な比較・置換機能はないため、書き込み直前に文字を比較しますが、
Windows のように他アプリとの競合を完全には防げません。

自動貼り付けは初期オフで、XTest による Ctrl+V を使用し、修飾キーを離した後にもフォーカスを確認します。
同じウィンドウ内での選択変更は確実に検出できないため、必要に応じて手動で貼り付けてください。

### Wayland での操作

最初の対象は GNOME 48+ と、GlobalShortcuts・RemoteDesktop キーボード・Clipboard portal です。
実機で確認済みなのは指定した Ubuntu 26.04／GNOME 50.1 の VM で、GNOME 48／49、KDE、wlroots は
未検証です。バージョン番号だけでは対応を判断できないため、起動時に必要な portal の機能を確認します。

1. ロックを解除した GUI セッションで起動します。ショートカットの許可画面で使うキーを選んで許可し、
   続いてキーボード・クリップボード操作を許可します。portal の **RemoteDesktop** という名前は、
   ここではローカルのキー入力とクリップボード操作を指します。ネットワーク接続や画面共有ではなく、
   アプリが要求するのはキーボード操作で、ポインター操作や画面取得は要求しません。
2. Settings または Quick start で、デスクトップが決定した**実際のショートカット**を確認します。
   `config.json` には希望したキーが残るため、有効なキーと異なる場合があります。
3. テキストを選択して実際のショートカットを押し、修飾キーを離します。選択範囲をコピーしている間は
   同じアプリに留まってください。Wayland では Ctrl+C を送ってコピーしますが、最前面ウィンドウの
   識別や物理的な修飾キーの状態確認はできません。長押し、フォーカスの変更、Ctrl+C を独自処理する
   アプリなどにより、コピーに失敗したり別のテキストを取得したりする場合があります。その場合は
   自分で必要なテキストをコピーし、Ctrl+C を送らない `input: "clipboard"` のプロファイルを使います。
4. 翻訳が準備できたら、自分で Ctrl+V を押します。`auto_paste` を `true` で保存していても、
   **Wayland では自動貼り付けは常に無効**です。保存値はほかのデスクトップセッション向けに維持します。
   クリップボード取得も RemoteDesktop セッション経由なので、クリップボード入力のプロファイルでも
   キーボード・クリップボード操作の許可が必要です。

新しくコピーした内容を守るため、書き込み前に文字を比較します。ただし Wayland でも X11 と同様に、
比較と書き込みは不可分な操作ではなく、競合を完全には防げません。貼り付けるまではアプリを起動したままに
してください。クリップボードマネージャーがない場合、終了時に内容が失われることがあります。

許可の拒否・取り消し・中断があってもアプリは終了せず、操作ウィンドウと Settings に理由を表示します。
有効になっていれば、操作ウィンドウの **Open menu > Retry Wayland permissions**、またはトレイの
同名メニューで再試行できます。GNOME がロックされている場合は、解除してから再試行してください。
必要な portal がない場合は対応デスクトップか Xorg セッションが必要で、許可を再試行しても不足機能は
追加されません。デスクトップが認めれば保存済みの許可を再起動時に使えますが、許可の取り消しや復元の失敗後は
再び許可画面が出る場合があります。

アプリ登録は `~/.local/share/applications/io.github.ypsilonmeister.ClipboardTranslator.desktop`
（`XDG_DATA_HOME` があればその下）に保存します。再利用する入力操作の許可情報は、Linux アプリの
`config.json` と同じフォルダー内の `wayland/restore-token` に、共有する設定とは分けて保存します。
フォルダーは mode 0700、ファイルは mode 0600 です。このフォルダーを不具合報告に添付しないでください。
許可トークンやクリップボードの文字はログに記録しません。ショートカットの割り当てはデスクトップ側が保存します。
実行ファイルを移動した場合は次回起動時にアプリ登録を更新します。Start at login は下記の手順で別途更新します。

受け入れ検証の手順と、確認済みの portal 操作・未実施のパッケージ全体の検証の区別は
[Wayland 手動検証シート（英語）](https://github.com/ypsilonmeister/clipboard-translator/blob/main/docs/wayland-manual-test.md)を参照してください。

### 操作ウィンドウ・自動起動・ビルド

トレイとメニューは GTK 3 で表示します。互換性のあるトレイ領域がない環境では、小さな操作ウィンドウを
表示し、メニューや Quit を使えるようにします。通知には任意で `notify-send` を使用します。
操作ウィンドウは開いたままにしてください。閉じるとアプリが終了します。
**Start at login** は X11 では `~/.config/autostart/clipboard-translator.desktop`、Wayland では
`~/.config/autostart/io.github.ypsilonmeister.ClipboardTranslator.desktop` に書き込みます
（`XDG_CONFIG_HOME` があればその下）。セッションを切り替えても両形式の自身の登録を認識し、オフにするときは
この実行ファイルに一致する登録だけを削除します。移動後は一度オフにしてからオンにし直してください。
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
fake portal テストと、明示的に有効化する Wayland テストコードのコンパイルも行いますが、
CI でデスクトップの許可操作や実際の Wayland セッションは検証しません。翻訳品質、Wayland の
パッケージ全体の操作、実際のデスクトップでのトレイ表示、GPU ドライバーは手動検証が必要です。

## ChromeOS（実験的対応、Crostini）

まず ChromeOS の Linux 開発環境に Linux 版をインストールし、モデルとランタイムをセットアップします。翻訳処理はコンテナ内で実行し、ブラウザ拡張機能がホスト側の操作を受け持ちます。

1. アプリの **Settings...** で **ChromeOS / local translation endpoint** を有効にし、**Copy token** でトークンをコピーします。初期状態では無効です。インストールごとのトークンはローカルで生成され、`config.json` の `translate_api_token` に保存されます。他人に共有しないでください。スイッチ（`translate_api_enabled`）は即時反映され、再起動後も保持されます。
2. ChromeOS の **設定 → デベロッパー → Linux 開発環境 → ポート転送** で TCP **18121** を追加して有効にします。拡張機能には `http://localhost:18121/translate` を指定します。`http://penguin.linux.test:18121/translate` も使用できます。
3. リポジトリの `extension/` ディレクトリを Chrome から選択できる場所（マイファイルなど）にコピーします。`chrome://extensions` で **デベロッパーモード** を有効にし、**パッケージ化されていない拡張機能を読み込む** でそのディレクトリを指定します。ビルドは不要です。
4. Linux の Crostini 内で GlobalShortcuts、RemoteDesktop のキーボード対応、Clipboard ポータルのいずれかの欠如を確認した場合、X11 と Wayland の両方でアプリメニューの **Chrome extension endpoint** と **Copy connection info** が使えます。ChromeOS 向けクイックスタートと Settings の表示もバックエンドの同じ機能判定を使います。Crostini は DMI 製品名 `crosvm`、`/dev/.cros_milestone`、`SOMMELIER_VERSION` のいずれかで検出し、通常の Linux デスクトップにはこのプロファイルを適用しません。X11 の判定が不確定な場合（2 秒のタイムアウトを含む）は警告を記録し、通常のプロファイルを維持します。このプロファイルでは Hotkeys の代わりに Languages を表示し、Translate clipboard とプロファイル指定なしのエンドポイントが使う先頭プロファイルの翻訳モードと言語を編集できます。保存時はその言語フィールドだけを更新し、キー割り当てや他のプロファイルを保持します。コントロールウィンドウの ready 表示にはホットキー名を含めません。X11 のホットキーとクリップボード動作は変わりません。翻訳中は接続情報のコピーを受け付けません。コピー後のクリップボードにはトークンが平文で含まれます。コピーした JSON を拡張機能の **Options** の **Paste connection info** に貼り付け、**Apply connection info**、**Test connection** を押します。URL とトークンの手動入力と **Save** も引き続き使えます。接続テストは「Hello」をローカルで翻訳し、クリップボードは変更しません。
5. Chrome で英語または日本語を選択し、右クリックして **Translate with Clipboard Translator** を選びます。通知に結果の短いプレビューが表示されたら **Ctrl+V** で貼り付けます。既定のプロファイルでは英語→日本語、日本語→英語になります。

拡張機能は先頭のプロファイルの翻訳方向、改行整形、置換／対訳出力を使用します。クリップボードの監視は行わず、設定したローカルエンドポイント以外へテキストを送りません。設定は Chrome 内にローカル保存し、同期しません。エンドポイントはホットキーと同じモデル、再ロード、分割翻訳処理を使用し、別の翻訳中はビジーを返します。Linux 側のクリップボードは変更せず、成功後に拡張機能がホスト側のクリップボードへ書き込みます。

必要な Wayland ポータルがない場合も、アプリは **manual mode** のコントロールウィンドウを開いたままにし、ホットキー操作が利用できないことを説明します。明示的にテキストをコピーし、コントロールウィンドウにフォーカスを移して **Open menu → Translate clipboard** を選び、完了後に貼り付けます。この操作は CLIPBOARD の内容を先頭のプロファイルで翻訳し、途中でコピーした新しい内容を保護します。自動貼り付けは行いません。Wayland のクリップボードアクセス許可のため、完了までコントロールウィンドウのフォーカスを維持してください。ポータルが揃っている環境では従来どおり動作します。

**制限:** ChromeOS はホストアプリと Linux コンテナの入力を設計上分離しているため、ホストアプリにフォーカスがある間は Linux のホットキーで翻訳を開始できません。この拡張機能は Chrome の選択テキストを対象とします。ホストのネイティブアプリでは手動のコピー→翻訳→貼り付けを使用します。拡張機能の読み込みと両方向の翻訳は、実機 Chromebook の Chrome 上での受け入れ確認がまだ必要です。

任意のエンドポイントは **Linux 専用**で、**平文 HTTP** を使います。ChromeOS/Linux 環境の**信頼できるネットワーク**での使用を想定し、通常の Linux ではループバックの IPv4 ポート **18121** のみ、Crostini では ChromeOS の転送用にコンテナのプライマリ IPv4 アドレスでも待ち受けます。アドレスを取得できない場合は理由をログに記録し、`0.0.0.0` にフォールバックします。推論エンジンと設定ページは引き続きループバック限定です。Crostini ではホストのネットワーク／ファイアウォール設定次第でコンテナのアドレスに接続でき、フォールバック時はすべての IPv4 インターフェースで接続を受け付けます。全リクエストに `X-Clipboard-Translator-Token` が必要です。転送は自分のマシンに限定してください。通常の Web ページへの CORS 許可はありません。無効にすると待ち受けを終了します。漏洩したトークンを変更するには、アプリを終了し、`config.json` の `translate_api_token` を削除して再起動し、拡張機能の設定も更新します。

ほかのローカルクライアントでは、トークンヘッダーを付けて `/translate` に JSON `{"text":"Hello","profile":"0"}` を POST します。`profile` は省略可能（先頭が既定）で、`Ctrl+Alt+E` など設定済みホットキー、または 0 始まりのインデックス文字列です。成功時は `{"translation":"…"}`、トークンなし／不一致は HTTP 401 です。API にダウンロード、設定変更、外部翻訳への中継機能はありません。

権限と対応ブラウザの詳細は [extension/README.md](extension/README.md) を参照してください。

## 使い方

アプリは**システムトレイ**(通知領域)に常駐します。Windows 11 では `^` の中に隠れていることが
あります。初めてモデルの読み込みが終わると「Ready」の吹き出しが出て、実際のホットキーを使った
3 ステップのクイックスタート(テキストを選択、ホットキーを押す、`Ctrl+V` で貼り付け。macOS では
`Cmd+V`)を表示します。表示は 1 回だけで、以後の起動では出ません。もう一度見るには、トレイの
**Quick start** か、設定ページ上部のカードを使います。ホットキーを登録できなかった場合は、
使えないショートカットを案内する代わりに、設定ページで別のホットキーを選ぶよう案内します。
このガイドは設定を変更しません(自動貼り付けはオフのままです)。ツールチップには状態と、
読み込み中・翻訳中の経過秒数が出ます。

日本語または英語のテキストを選択して `Ctrl+Alt+E` を押します。翻訳が終わると、結果が
クリップボードに入るので、`Ctrl+V` で貼り付けます。何も選択していないときは、
クリップボードにあるテキストを翻訳します(Windows では、反応の遅いページのコピーを待つため、
最大 2 秒後になります。`"input": "clipboard"` のプロファイルはこの選択コピーの待ち時間を省きます)。

トレイアイコンを右クリックすると、次のメニューが出ます(表示はすべて英語です)。

| 項目 | 内容 |
| --- | --- |
| **Settings...** | ブラウザで設定ページを開きます。ホットキー(キー、言語のペアまたは翻訳先の言語)の追加・変更・削除、自動貼り付け、完了通知、翻訳する文字数の上限、アイドル時のモデルのアンロード、翻訳モデル(サイズのダウンロードと、導入済みのモデルの切り替え)、エンジンを設定できます。`config.json` に保存されます(「設定ページ」を見てください)。 |
| **Quick start** | 現在のホットキーで、選択・ホットキー・貼り付けの手順をもう一度表示します。 |
| **Cancel translation** | 翻訳中だけ表示されます。 |
| **Unload model** | 翻訳やモデルの切り替え中でない場合に、サーバーを停止してモデルをアンロードします。 |
| **Translation model** | 2つ以上のモデルがインストールされている場合に表示されます。速度重視のモデルと精度重視のモデルを素早く切り替えます。 |
| **Recent translations** | 直近 10 件。メモリ上だけに保持し、ディスクには書きません。項目をクリックすると訳文をコピーします。「Copy original or both」から履歴を選ぶと、原文または原文＋訳文をコピーできます。翻訳中に別の内容をコピーして自動反映できなかった訳文も、ここから取り出せます。*Clear history* で空にします。 |
| **Check for updates** | クリックしたときだけ GitHub に最新リリースを問い合わせます。アプリが勝手に確認することはありません。 |
| **Open config.json** | そのほかの設定は[設定](#設定)を見てください。 |
| **Built with PLaMo (license)** | PLaMo コミュニティライセンスを開きます。 |
| **Start with Windows** | オン/オフの切り替え。 |
| **Open log** / **Quit** | |

### トレイにあるもの / Settings にあるもの

| 場所 | 操作・設定 |
| --- | --- |
| トレイ | Settings、翻訳の中止、モデルのアンロード、翻訳モデル（2つ以上のモデルが導入されている場合。速度重視と精度重視のモデルの素早い切り替え）、最近の翻訳、更新、config.json を開く、ログイン時に起動、ライセンス、終了（ログとバージョン表示もあります） |
| Settings | ホットキーと言語、出力形式（置換／原文＋訳文の併記）とテキストの取得元（選択／クリップボード）（いずれもプロファイルごと）、自動貼り付け、完了通知、モデルとエンジンの選択、文字数上限、アイドル時アンロード |

自動貼り付けは初期状態ではオフです。対応デスクトップでオンにした場合も、新しい選択範囲を翻訳し、同じウィンドウが手前にある場合だけ貼り付けます。それ以外はクリップボードで待ちます。Wayland では常に無効です。
ホットキーを登録できなかった場合も、トレイの先頭の「Settings...」から開き、別のホットキーを選んで「Save」を押せます。
高度な設定は「Open config.json」で編集し、再起動してください。

翻訳サーバー(`llama-server`)が予期せず止まったときは、自動で再起動します(1 秒、2 秒、4 秒...と間隔を空けます)。
10 分以内に 3 回再起動しても直らなければ、メッセージを出して終了します。起動時のエラーはメッセージボックスに出て、
`translator.log` にも書かれます。

## プライバシーとネットワーク

- 翻訳は手元の PC で行います。推論サーバーは `127.0.0.1` だけで待ち受けるので、ほかのコンピューターからは接続できません。
  ただし同じ PC のプログラム(ブラウザで開いた Web ページを含む)は `127.0.0.1` に接続できるため、
  アプリはサーバーを起動するたびにランダムなキーを渡し、サーバーはそのキーのない翻訳などの API リクエストを拒否します
  (状態確認用の `/health` はキーなしでも応答します)。llama-server に組み込みの Web UI も無効にするよう指定しています
  (`LLAMA_ARG_UI=0`。現在の llama.cpp のビルドが読み取ります)。
- アプリがネットワークに接続するのは、操作したときだけです。モデルと llama.cpp のダウンロード
  (`Setup.cmd`、`setup.ps1`、初回起動時の提案、インストールスクリプト。モデルと llama.cpp はどちらも固定バージョンをダウンロードし、ピン留めされた SHA-256 と照合します)と、「Check for updates」「Update」です。
  テレメトリーも、バックグラウンドでの確認もありません。
- 「Settings...」は、アプリ自身が `127.0.0.1` で提供するページを開きます(空いているポートで、ページを開いている間だけ。
  最後のアクセスから数分後か、「Close this page」で閉じます)。アドレスに含まれるランダムなトークンで保護され、
  インターネットからは何も読み込みません。
- 設定ページはモデルをダウンロードできます。取得するのはそれだけで、「Download」を押したときだけ、
  `huggingface.co` から、そのモデルの固定したリビジョンだけを取得し、SHA-256 が固定値と一致したファイルだけを使います。
  ダウンロードはページを閉じても続き、ページから中止できます。中止や失敗のとき、途中までのファイルは残り、
  次回はその続きから再開します。
- 任意の ChromeOS 翻訳エンドポイントは初期状態では無効です。Linux 専用で、有効時はループバック（Crostini ではコンテナのアドレスも。取得失敗時はログを残してワイルドカードにフォールバック）の IPv4 ポート 18121 で平文 HTTP を使い、トークン認証を必須とします。ChromeOS/Linux 環境の信頼できるネットワークで使用してください。待ち受けアドレスと転送範囲は ChromeOS の節を参照してください。外向きの通信は行いません。
- ログにはエラーを記録しますが、翻訳したテキストは記録しません。翻訳履歴はメモリ上だけにあり、
  アプリを終了すると消えます。

## 設定

普段の設定は Settings で変更します。高度な設定は `config.json` を手で編集し、再起動してください
(パスはすべてアプリのフォルダーからの相対パスです)。
変更前のファイルは `config.json.bak` として残ります。

| キー | 内容 |
| --- | --- |
| `hotkey` | 修飾キー + 1 キー（A〜Z の文字、0〜9 の数字、または F1〜F12 のファンクションキー）。例: `Ctrl+Alt+E`、`Ctrl+F5`（macOS では `Cmd` / `Command` と `Option` も使えます） |
| `auto_paste` | 対応デスクトップでは `true` で翻訳後に Ctrl+V を自動入力（初期値は `false`。Wayland では常に無効） |
| `quickstart_shown` | 初回のクイックスタートを表示済みなら `true`(アプリが設定します。`false` に戻すと吹き出しがもう一度出ます。初期値は `false`) |
| `notify_on_complete` | `true` で自動貼り付けしない翻訳の完了時に、テキストは表示せずクリップボードに準備できたことをトレイ通知(初期値は `false`) |
| `languages` | 双方向に翻訳する 2 つの言語コード。例: `["ja", "en"]`(初期値)、`["en", "ko"]`、`["ja", "zh-hans"]`。コードはモデルのものです: `ja en ko zh-hans zh-hant ru th ar de fr es ...`(全 53 言語。[モデルカード](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF)を見てください)。翻訳の向きはテキストの文字種から決めるため、2 つの言語は別の文字を使うものにしてください(en ↔ de は今のところ拒否されます)。主な用途は日本語 ↔ 英語で、ほかの言語はベストエフォートです。 |
| `profiles` | ホットキーごとに言語を割り当てます。例: `[{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]}, {"hotkey": "Ctrl+Alt+K", "target": "ko", "output": "bilingual"}]`。各プロファイルには `languages`(ペア。双方向)か `target`(常にその言語へ翻訳。原文の言語はモデルが判定するので、どの言語でも使えます。`en` と `de` のように文字が同じペアは、`target` のプロファイルを1つずつ作ってください)のどちらか一方を書きます。また、出力形式として `output` を指定できます: `"replace"`(デフォルト。省略時もこれ)は翻訳文のみをクリップボードに入れます。`"bilingual"` は原文、改行、翻訳文の順でクリップボードに入れます(`<source>\n<translated>`。原文末尾の改行文字 CR/LF は取り除いてから 1 つの改行 `\n` を付加します)。翻訳履歴には原文と翻訳文が個別に保持され、「Recent translations」メニューから原文・訳文・両方を選んでコピーできます。`input` で、テキストをどこから取るかを選べます: `"selection"`(デフォルト)は、まず選択範囲をコピー(Ctrl+C)します(Windows では、反応の遅いページのために最大 2 秒待ちます)。`"clipboard"` は、すでにクリップボードにあるテキストを翻訳します。Ctrl+C を送らず、選択コピーの待ち時間を省き、自動貼り付けもしないため、コピーが遅いページ向けです（Wayland のクリップボード転送は非同期です）(自分でコピーしてからホットキーを押します)。`hotkey` と `languages` の代わりになり、Settings では各プロファイルを編集できます。Windows と Linux では最大 8 個。macOS では今のところ最初の 1 個だけ登録し、有効にならなかったものは通知で知らせます。他のプログラムが使っているホットキーは、そのプロファイルだけが無効になります。 |
| `port` | ローカルの llama-server のポート(1024〜65534。アプリは `port + 1` も多重起動の防止に使います) |
| `max_chars` | これより長いテキストは翻訳しません |
| `idle_unload_minutes` | アイドル時にモデルをアンロードするまでの分数。0〜1440、初期値 `0` は無効。設定ページでも変更できます。 |
| `gpu_layers` | GPU に載せる層の数(`0` は CPU のみ) |
| `model_file` | 使うモデル。例: `models/plamo-3-translate-8b-Q4_K_M.gguf`。`setup.ps1 -Model` が設定します(「モデルのサイズを選ぶ」を見てください) |
| `server_file` / `fallback_server_file` | llama-server の実行ファイル(通常用 / CPU の予備) |

> `setup.ps1` が `server_file`、`fallback_server_file`、`gpu_layers` を検出したバックエンドに合わせて設定するのは、
> 新規インストールのとき、`-Backend cuda|vulkan|cpu` を明示したとき、GPU 版を新しくインストールしたときです。
> 通常の再実行や更新(Scoop のように `-Backend auto`)では、使用中のエンジンと `gpu_layers` はそのまま残ります。
> 不足している、または不完全なバックエンドだけをダウンロードし、すべてのダウンロードを SHA-256 で検証します
> (モデルファイルは特定のリビジョンに固定しています)。

## 設定ページ

設定ページでは **Unload model after idle** (`idle_unload_minutes`) を変更し、「Save」で保存できます。手動または自動のアンロードは RAM/VRAM を解放します。次のホットキー操作で自動的にモデルを読み込み直して翻訳するため、その翻訳は読み込み時間の分だけ遅くなります。

トレイメニューの「Settings...」で、ブラウザに開きます(提供のしかたは「プライバシーとネットワーク」を見てください)。
次の設定ができます。

- **ホットキー**(キー、言語のペアまたは翻訳先の言語、**テキストの取得元**(選択範囲／クリップボード。`input`)、および**出力形式**(「Replace with the translation」/「Keep the original, then the translation」)。`config.json` の `profiles`)、**自動貼り付け**、**完了通知**(`notify_on_complete`)、
  **翻訳する文字数の上限**(`max_chars`)。これらは「Save」で反映されます。
- **翻訳モデル**: 「Download」で、まだ持っていないサイズを取得します(進捗表示と「Cancel」つき。31B は選べません)。
  「Use this model」で、導入済みのモデルへ切り替えます。切り替えでは `llama-server` を起動し直すので、少し時間が
  かかります。新しいモデルを読み込めなかったとき(メモリ不足など)は、前のモデルへの復元を試み、ページに結果を表示します。
  復元にも失敗した場合は、翻訳サーバーが停止し、再起動中であることを通知します。
  「Delete」で使っていない導入済みモデルや不完全なファイルを削除でき(確認ボタンつき)、「Remove partial download」で中断した
  ダウンロードの残骸を削除してディスク容量を解放できます(導入済みや使用中のモデルに対しても実行可能)。使用中のモデル本体とチャットテンプレートは削除されません。
- **エンジン**(CPU、CUDA、Vulkan。macOS では Metal): 2 種類以上入っているときだけ選べます。

そのほかの設定は `config.json` に残ります。

## 制限事項

- アプリによっては、選択範囲を読むための擬似的な Ctrl+C を受け付けません。
- 選択範囲を読むとクリップボードが変わります。翻訳結果は、クリップボードのテキストが取得時のまま
  のときだけ、それを置き換えます。
- 1 つのホットキーで使える言語のペアは 1 つで、2 つの言語は別の文字を使うものに限ります(en ↔ de や、簡体字 ↔ 繁体字は、文字から向きを判別できません。それぞれ `target` のプロファイルを作ってください)。日本語と中国語を同じペアにした場合、漢字だけのテキストは中国語、かなが入っていれば日本語として扱います。URL は翻訳しません。翻訳できないテキスト（未選択、URL のみ、言語ペアと異なる文字体系）はトレイのツールチップで理由を表示し、文字数上限を超えた場合は通知でも知らせます。
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
- `.github/workflows/llama-smoke.yml` は、週次および PR/手動実行で、固定された Linux CPU 向け `llama-server` と
  0.8B モデルを用いて実際の起動と翻訳を検証するスモークテストを実行します。
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
