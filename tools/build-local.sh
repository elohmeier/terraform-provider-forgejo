#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
version=1.6.1-ops.1
platform="$(go env GOOS)_$(go env GOARCH)"
mirror="$repo_root/bin/mirror"
target="$mirror/registry.opentofu.org/elohmeier/forgejo/$version/$platform"
mkdir -p "$target"
go build -ldflags "-X main.version=$version" -o "$target/terraform-provider-forgejo_v$version" .
cat > "$repo_root/bin/tofurc" <<CONFIG
provider_installation {
  filesystem_mirror {
    path = "$mirror"
    include = ["registry.opentofu.org/elohmeier/forgejo"]
  }
  direct {
    exclude = ["registry.opentofu.org/elohmeier/forgejo"]
  }
}
CONFIG
printf 'Local fork built. Use TF_CLI_CONFIG_FILE=%s/bin/tofurc with source elohmeier/forgejo and version %s.\n' "$repo_root" "$version"
