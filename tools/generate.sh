#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
mkdir -p "$tmpdir/bin" "$tmpdir/config"
cd "$repo_root"
tofu fmt -recursive examples/
go build -o "$tmpdir/bin/terraform-provider-forgejo" .
# tfplugindocs v0.19 expects this schema key. It is a temporary, local-only
# identity used for schema extraction, not the installation address of the fork.
cat > "$tmpdir/tofurc" <<CONFIG
provider_installation {
  dev_overrides {
    "registry.terraform.io/hashicorp/forgejo" = "$tmpdir/bin"
  }
  direct {}
}
CONFIG
cat > "$tmpdir/config/main.tf" <<'CONFIG'
terraform {
  required_providers {
    forgejo = { source = "registry.terraform.io/hashicorp/forgejo" }
  }
}
CONFIG
TF_CLI_CONFIG_FILE="$tmpdir/tofurc" tofu -chdir="$tmpdir/config" providers schema -json > "$tmpdir/schema.json"
go -C tools run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate \
  --provider-dir "$repo_root" --provider-name forgejo --providers-schema "$tmpdir/schema.json"
