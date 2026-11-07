#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
version=${1:-dev}
if [[ ! $version =~ ^[A-Za-z0-9.+_-]+$ ]]; then echo 'Invalid version' >&2; exit 2; fi
arch=$(go env GOARCH)
name="clipboard-translator-linux-$arch-$version"
mkdir -p dist
# A fresh staging directory prevents stale files from entering the archive.
stage=$(mktemp -d "$PWD/dist/.linux-package-XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
mkdir "$stage/$name"
CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=${version#v}" -o "$stage/$name/clipboard-translator" .
cp setup.sh README.md README.ja.md LICENSE "$stage/$name/"
tar -C "$stage" -czf "dist/$name.tar.gz" "$name"
(cd dist && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
echo "Built dist/$name.tar.gz"
