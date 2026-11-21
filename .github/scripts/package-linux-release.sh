#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <version> <output-directory>" >&2
  exit 2
fi

version=$1
output_dir=$(realpath -m "$2")
archive="parchment_${version}_linux_amd64.tar.gz"

mkdir -p "$output_dir"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$output_dir/parchment" ./cmd/parchment
cp LICENSE "$output_dir/LICENSE"
tar -czf "$output_dir/$archive" -C "$output_dir" parchment LICENSE

(
  cd "$output_dir"
  sha256sum "$archive" > checksums.txt
  sha256sum -c checksums.txt
)

mkdir -p "$output_dir/smoke/home"
tar -xzf "$output_dir/$archive" -C "$output_dir/smoke"
test -x "$output_dir/smoke/parchment"
env -u PARCHMENT_CONFIG HOME="$output_dir/smoke/home" \
  "$output_dir/smoke/parchment" --help > /dev/null
env -u PARCHMENT_CONFIG HOME="$output_dir/smoke/home" \
  "$output_dir/smoke/parchment" note create "$output_dir/smoke/note.md" \
  --body "release smoke test" --no-edit > /dev/null
env -u PARCHMENT_CONFIG HOME="$output_dir/smoke/home" \
  "$output_dir/smoke/parchment" note show "$output_dir/smoke/note.md" | grep -Fx "release smoke test"
