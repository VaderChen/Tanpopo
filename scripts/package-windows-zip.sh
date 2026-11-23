#!/usr/bin/env bash
# 將已組裝的 Windows 發行目錄封裝成可攜 ZIP。
set -euo pipefail

payload=${1:?請指定 Tanpopo 發行目錄}
destination=${2:?請指定輸出目錄}
version=${3:?請指定版本}
build=${4:?請指定 build}
[[ "$(basename "$payload")" == Tanpopo ]]
for name in Tanpopo.exe netpass-client/NetPassClient.exe llama-runtime/prebuilt/windows-amd64/bin/llama-server.exe website/settings.html agent.sample.properties; do
  [[ -f "$payload/$name" ]] || { echo "發行目錄缺少 $name" >&2; exit 1; }
done
printf '%s\n' "$version" > "$payload/VERSION"
printf 'app=Tanpopo\nplatform=windows-amd64\napp_version=%s\napp_build=%s\nentry=Tanpopo.exe\n' "$version" "$build" > "$payload/BUILD_INFO.txt"
mkdir -p "$destination"
destination=$(cd "$destination" && pwd)
stage=$(mktemp -d "$destination/.windows-zip.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
name="Tanpopo-$version-build-$build-windows-x64.zip"
(cd "$(dirname "$payload")" && zip -qr "$stage/$name" Tanpopo)
unzip -tq "$stage/$name" >/dev/null
mv -f "$stage/$name" "$destination/$name"
