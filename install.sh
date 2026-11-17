#!/bin/sh
# Install/update: curl -fsSL https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/main/install.sh | sh
# Saved script: sh install.sh --help
set -eu
fail() { echo "Error: $*" >&2; exit 1; }
warn() { echo "Warning: $*" >&2; }
usage() {
    echo 'Usage: sh install.sh [--version VERSION] [--install-dir DIR] [--skip-setup] [--no-launch] [--uninstall] [--yes]'
    echo 'Environment: CT_VERSION, CT_INSTALL_DIR, CT_SKIP_SETUP=1, CT_NO_LAUNCH=1, CT_UNINSTALL=1, CT_YES=1'
}
# Wrap execution so a piped shell reads the whole script before any child uses stdin.
main() {
    version=${CT_VERSION:-}
    data_home=${XDG_DATA_HOME:-$HOME/.local/share}
    install_dir=${CT_INSTALL_DIR:-$data_home/clipboard-translator}
    skip_setup=${CT_SKIP_SETUP:-0}
    no_launch=${CT_NO_LAUNCH:-0}
    uninstall=${CT_UNINSTALL:-0}
    yes=${CT_YES:-0}
    while [ "$#" -gt 0 ]; do
        case $1 in
            --version|--install-dir)
                [ "$#" -ge 2 ] && [ -n "$2" ] || fail "$1 needs a value"
                case $1 in --version) version=$2 ;; --install-dir) install_dir=$2 ;; esac
                shift 2 ;;
            --skip-setup) skip_setup=1; shift ;;
            --no-launch) no_launch=1; shift ;;
            --uninstall) uninstall=1; shift ;;
            --yes) yes=1; shift ;;
            --help|-h) usage; return ;;
            *) usage >&2; fail "Unknown option: $1" ;;
        esac
    done
    [ "$(uname -s)" = Linux ] || fail 'This installer supports Linux only.'
    [ "$(uname -m)" = x86_64 ] || fail 'Linux x86_64 is required; no ARM64 release is published.'
    case $data_home in /*) ;; *) fail 'XDG_DATA_HOME must be absolute.' ;; esac
    case $install_dir in /*) ;; *) install_dir=$PWD/$install_dir ;; esac
    # realpath -m works for a new directory too, without creating it.
    install_dir=$(realpath -m -- "$install_dir")
    case $install_dir in /|/home|/usr|/usr/*|/etc|/etc/*|/bin|/sbin|/var|/opt|/tmp|"$HOME"|"$data_home") fail "Unsafe install directory: $install_dir" ;; esac
    # Desktop Exec has its own escaping rules; reject control characters in paths.
    cr=$(printf '\r')
    case $install_dir in *'
'*|*"$cr"*) fail 'Install directory must not contain line breaks.' ;; esac
    exe=$install_dir/clipboard-translator
    desktop=$data_home/applications/clipboard-translator.desktop
    desktop_exec=$(printf '%s' "$exe" | sed 's/\\/\\\\\\\\/g; s/"/\\\\"/g; s/`/\\\\`/g; s/\$/\\\\$/g; s/%/%%/g')
    owned='clipboard-translator setup.sh README.md README.ja.md LICENSE config.json config.json.bak translator.log server.log STOP .installed'
    if [ "$uninstall" = 1 ]; then
        [ -f "$exe" ] || fail "Clipboard Translator is not installed at $install_dir"
        if [ "$yes" != 1 ]; then
            printf 'Remove Clipboard Translator, its configuration, models and runtime from %s? [y/N] ' "$install_dir" >/dev/tty
            read -r answer </dev/tty || fail 'No confirmation; use --yes for unattended uninstall.'
            case $answer in y|Y|yes|YES) ;; *) echo 'Cancelled.'; return ;; esac
        fi
        stop_app
        # Registrations belong to an executable, not merely to this app's name.
        for entry in "$desktop" "$data_home/applications/io.github.ypsilonmeister.ClipboardTranslator.desktop" "${XDG_CONFIG_HOME:-$HOME/.config}/autostart/clipboard-translator.desktop" "${XDG_CONFIG_HOME:-$HOME/.config}/autostart/io.github.ypsilonmeister.ClipboardTranslator.desktop"; do
            if [ -f "$entry" ] && grep -Fxq "Exec=\"$desktop_exec\"" "$entry"; then rm -f -- "$entry"; fi
        done
        for name in $owned; do
            [ ! -d "$install_dir/$name" ] || continue
            rm -f -- "$install_dir/$name"
        done
        if [ ! -L "$install_dir/models" ]; then
            for name in plamo-3-translate-0.8b-Q4_K_M.gguf plamo-3-translate-2b-Q4_K_M.gguf plamo-3-translate-8b-Q4_K_M.gguf chat_template.jinja chat_template.0.8b.jinja chat_template.2b.jinja chat_template.8b.jinja; do
                rm -f -- "$install_dir/models/$name" "$install_dir/models/$name.partial"
            done
            rmdir -- "$install_dir/models" 2>/dev/null || :
        fi
        if [ ! -L "$install_dir/runtime" ]; then
            rm -rf -- "$install_dir/runtime/cpu" "$install_dir/runtime/vulkan"
            rmdir -- "$install_dir/runtime" 2>/dev/null || :
        fi
        if [ ! -L "$install_dir/wayland" ]; then
            rm -f -- "$install_dir/wayland/restore-token"
            rmdir -- "$install_dir/wayland" 2>/dev/null || :
        fi
        rmdir -- "$install_dir" 2>/dev/null || :
        echo 'Clipboard Translator removed; unrelated files, if any, were left alone.'
        return
    fi
    if [ -d "$install_dir" ] && [ ! -f "$exe" ] && [ ! -f "$install_dir/.installed" ] && [ -n "$(ls -A -- "$install_dir")" ]; then
        fail "$install_dir is not empty and is not a Clipboard Translator folder. Use a dedicated empty or new directory."
    fi
    for tool in curl tar sha256sum bash nohup; do command -v "$tool" >/dev/null 2>&1 || fail "Install required command: $tool"; done
    tmp=$(mktemp -d)
    trap 'rm -rf -- "$tmp"' EXIT
    trap 'exit 1' HUP INT TERM
    repo=https://github.com/ypsilonmeister/clipboard-translator
    if [ -z "$version" ]; then
        curl -fsSL https://api.github.com/repos/ypsilonmeister/clipboard-translator/releases/latest -o "$tmp/release.json"
        version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp/release.json")
    fi
    version=${version#v}
    case $version in ''|*[!0-9A-Za-z.+_-]*) fail 'Could not resolve a valid release version.' ;; esac
    name=clipboard-translator-linux-amd64-v$version
    url=$repo/releases/download/v$version/$name.tar.gz
    echo "Downloading $name"
    curl -fsSL "$url" -o "$tmp/app.tar.gz"
    curl -fsSL "$url.sha256" -o "$tmp/app.sha256"
    expected=$(awk 'NR == 1 {print tolower($1)}' "$tmp/app.sha256")
    [ "${#expected}" = 64 ] || fail 'Invalid published SHA-256.'
    case $expected in *[!0-9a-f]*) fail 'Invalid published SHA-256.' ;; esac
    actual=$(sha256sum "$tmp/app.tar.gz")
    actual=${actual%% *}
    [ "$actual" = "$expected" ] || fail 'SHA-256 mismatch; install directory has not been modified.'
    echo "SHA-256 verified: $actual"
    # Releases contain a single folder of regular files. Reject paths and links
    # rather than allowing an archive to write outside the staging directory.
    tar -tzf "$tmp/app.tar.gz" > "$tmp/list"
    while IFS= read -r member; do
        case $member in
            "$name/"|"$name/clipboard-translator"|"$name/setup.sh"|"$name/README.md"|"$name/README.ja.md"|"$name/LICENSE"|"$name/config.json") ;;
            *) fail "Unexpected archive entry: $member" ;;
        esac
    done < "$tmp/list"
    tar -tvzf "$tmp/app.tar.gz" > "$tmp/types"
    if grep -v '^[d-]' "$tmp/types" >/dev/null; then fail 'Archive contains links or special files.'; fi
    mkdir "$tmp/tree"
    tar -xzf "$tmp/app.tar.gz" --no-same-owner -C "$tmp/tree"
    tree=$tmp/tree/$name
    [ -x "$tree/clipboard-translator" ] && [ -f "$tree/setup.sh" ] || fail 'Release lacks the executable or setup.sh.'
    apt_command='sudo apt-get install libgtk-3-0t64 libxtst6 xdg-utils libnotify-bin'
    if ! dependencies=$(ldd "$tree/clipboard-translator" 2>&1) || printf '%s\n' "$dependencies" | grep -q 'not found'; then
        warn "Runtime libraries are missing or could not be checked. On Ubuntu 24.04 run: $apt_command"
    fi
    if ! command -v xdg-open >/dev/null 2>&1 || ! command -v notify-send >/dev/null 2>&1; then
        warn "Desktop helpers are missing. On Ubuntu 24.04 run: $apt_command"
    fi
    session=${XDG_SESSION_TYPE:-}
    if [ -z "$session" ]; then
        if [ -n "${WAYLAND_DISPLAY:-}" ]; then session=wayland
        elif [ -n "${DISPLAY:-}" ]; then session=x11; fi
    fi
    case $session in
        x11) [ -n "${DISPLAY:-}" ] || warn 'No X11 display; start the app from a graphical X11 session.' ;;
        wayland)
            portals=''
            devices=0
            if command -v gdbus >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
                portals=$(timeout 5 gdbus introspect --session --dest org.freedesktop.portal.Desktop --object-path /org/freedesktop/portal/desktop 2>/dev/null) || portals=''
                property=$(gdbus call --session --dest org.freedesktop.portal.Desktop --object-path /org/freedesktop/portal/desktop --timeout 5 --method org.freedesktop.DBus.Properties.Get org.freedesktop.portal.RemoteDesktop SupportedDeviceTypes 2>/dev/null) || property=''
                devices=$(printf '%s' "$property" | sed -n 's/.*uint32 \([0-9][0-9]*\).*/\1/p')
                case $devices in ''|*[!0-9]*) devices=0 ;; esac
            fi
            if ! printf '%s' "$portals" | grep -q 'org.freedesktop.portal.GlobalShortcuts' ||
               ! printf '%s' "$portals" | grep -q 'org.freedesktop.portal.RemoteDesktop' ||
               ! printf '%s' "$portals" | grep -q 'org.freedesktop.portal.Clipboard' || [ "$((devices & 1))" = 0 ]; then
                warn 'Wayland requires GlobalShortcuts, RemoteDesktop keyboard and Clipboard portals (initial target GNOME 48+). Could not confirm them; use a compatible desktop or Xorg. The app checks capabilities at startup.'
            fi ;;
        *) warn 'No supported graphical session detected. Use X11 or Wayland with GlobalShortcuts, RemoteDesktop keyboard and Clipboard portals.' ;;
    esac
    for file in $owned models runtime wayland; do
        [ ! -L "$install_dir/$file" ] || fail "Symlink in install directory: $file"
    done
    stop_app
    mkdir -p -- "$install_dir"
    for source in "$tree"/*; do
        file=${source##*/}
        [ "$file" != config.json ] || [ ! -e "$install_dir/config.json" ] || continue
        [ ! -L "$install_dir/$file" ] && [ ! -d "$install_dir/$file" ] || fail "Unsafe existing destination: $file"
        # Rename avoids overwriting a running executable inode.
        staged=$(mktemp "$install_dir/.install-XXXXXX")
        cp -p -- "$source" "$staged"
        mv -f -- "$staged" "$install_dir/$file"
    done
    rm -f -- "$install_dir/.installed"
    printf '%s\n' 'Clipboard Translator Linux installer' > "$install_dir/.installed"
    if [ "$skip_setup" != 1 ]; then
        bash "$install_dir/setup.sh" </dev/null
    else
        echo "Skipping setup. Run: bash \"$install_dir/setup.sh\""
    fi
    mkdir -p -- "$data_home/applications"
    desktop_tmp=$(mktemp "$data_home/applications/.clipboard-translator-XXXXXX")
    cat > "$desktop_tmp" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Clipboard Translator
Exec="$desktop_exec"
Icon=accessories-dictionary
Terminal=false
Categories=Utility;
DESKTOP
    chmod 644 "$desktop_tmp"
    mv -f -- "$desktop_tmp" "$desktop"
    if [ "$no_launch" != 1 ]; then
        (cd -- "$install_dir" && nohup "$exe" </dev/null >>translator.log 2>&1 &)
    fi
    echo "Installed $version in $install_dir. Select text, press your hotkey (default Ctrl+Alt+E), then paste with Ctrl+V."
    echo 'Update: run the installer again. Remove: sh install.sh --uninstall (same --install-dir).'
}
stop_app() {
    [ -d "$install_dir" ] || return 0
    [ ! -L "$install_dir/STOP" ] || fail 'STOP must not be a symlink.'
    # Match the full executable path; never signal another installation.
    for proc in /proc/[0-9]*/exe; do
        target=$(readlink "$proc" 2>/dev/null) || continue
        [ "$target" = "$exe" ] || continue
        touch "$install_dir/STOP"
        count=0
        while [ "$(readlink "$proc" 2>/dev/null || :)" = "$exe" ]; do
            [ "$count" -lt 15 ] || fail 'App did not stop. Quit it and run the installer again.'
            sleep 1
            count=$((count + 1))
        done
    done
    rm -f -- "$install_dir/STOP"
}
main "$@"
