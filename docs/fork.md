# Declarative management fork

This fork is based on `svalabs/forgejo` v1.6.1. It is intended for adopting an
existing Forgejo installation under OpenTofu without recreating its repositories,
organisations or users. Research and validation were performed on 2026-09-21.
The fork has not been published to either provider registry.

## Open upstream PR review

| PR | Decision |
| --- | --- |
| [#155: team repository resource](https://github.com/svalabs/terraform-provider-forgejo/pull/155) | Cherry-picked with original authorship. Adapted to `team_id` and `repository_id` as requested by the maintainer; uses the direct membership endpoint, supports `org/team/repo` import, detects deletion and tolerates already-absent grants on destroy. Added acceptance tests. |
| [#178: pagination](https://github.com/svalabs/terraform-provider-forgejo/pull/178) | Cherry-picked with original authorship. Also paginated the token data source introduced after that PR. Added short-page unit regressions and a live lookup among 55 teams. |
| [#186: team permissions](https://github.com/svalabs/terraform-provider-forgejo/pull/186) | Not cherry-picked as written: it has an acknowledged SDK validation problem and removes existing configuration forms. Removed the default `permission = "read"`; when omitted, a valid SDK request lets Forgejo derive the effective permission from `units_map`. Existing explicit configurations remain supported. Tested granular read/write units. |
| [#142: organisation visibility fallback](https://github.com/svalabs/terraform-provider-forgejo/pull/142) | Rejected after testing: the administrator fallback also fails to change private/limited visibility to public on Forgejo 15.0.1. Both API handlers use `optional.FromNonDefault` for the public/zero visibility value. The provider now fails with a specific diagnostic instead of claiming success or replacing the organisation. |
| [#199: mirror interval normalization](https://github.com/svalabs/terraform-provider-forgejo/pull/199) | Reviewed, not included: mirror migration/normalization is outside this adoption and access-management scope. |

[Upstream issue #166](https://github.com/svalabs/terraform-provider-forgejo/issues/166)
tracks missing importers. This fork implements the organisation, direct
collaborator and team-member importers needed for adoption, plus token metadata
import. Other key and Actions-variable importers are not added in this change.

## Added and improved behaviour

- `forgejo_team_repository`: explicit grants between numeric team and repository
  IDs. The team must use `includes_all_repositories = false` when creating the
  grant. Import with `org/team/repo`.
- `forgejo_organization`: import by name. Read the actual
  `repo_admin_change_team_access` value, including `false`, rather than guessing
  a default. Update that setting in place while preserving organisation profile
  fields. SDK v3 omits the field, so the provider uses a small authenticated API
  extension for it.
- `forgejo_team_member`: import with `org/team/username`; recreate a grant removed
  outside IaC.
- `forgejo_collaborator`: import with `owner/repository/username`; distinguish
  direct collaborator access from inherited access and recreate removed grants.
- `forgejo_user`: password is optional for adoption. Creating a local user still
  requires a password. Omitting the password from an imported resource preserves
  the existing credential. This does not make all other user settings readable;
  inspect the plan for the provider's defaults before adopting bots or humans.
- `forgejo_personal_access_token`: `repository_ids` restricts the token to specific
  repositories. Empty means no repository restriction. Changing restrictions or
  scopes replaces the token. Import `username/numeric-token-id` to adopt metadata;
  the original token value cannot be recovered. BasicAuth remains required by
  Forgejo for creation/deletion. Use repository/issue scopes with restricted
  tokens; broader scopes are rejected by Forgejo.
- `forgejo_oauth2_application`: CRUD and import by numeric application ID, owned
  by the authenticated user. Every API update regenerates the client secret;
  the plan marks it unknown, and the response supplies the new sensitive value.
  Reads preserve the stored secret. Import leaves it null because it cannot be
  recovered. Coordinate updates with Woodpecker or other consumers.
- Refresh removes absent organisations, teams, grants, OAuth applications and
  tokens from state so the next plan can recreate them. Permission errors and
  server errors fail while preserving state.

Token and OAuth secret resource values are stored in state. Use encrypted state
and plans; `sensitive` does not encrypt them. An imported credential is not a
usable secret output until it has been deliberately rotated or supplied to its
consumer through a separate secret-delivery path.

Memberships and collaborators are individual grants, not an authoritative policy
that deletes every undeclared user. Woodpecker-created webhooks should remain
owned by Woodpecker. Forgejo Actions secrets are distinct from Woodpecker secrets.

## Known server and bootstrap boundaries

Forgejo 15.0.1 can create a public organisation and make it private, but its API
cannot change an existing private/limited organisation back to public. The
provider detects this failure. Use the web interface for that transition or a
server version with the API bug fixed; no particular fixed version was verified.
The failing transition and subsequent recovery are covered by acceptance tests.

External authentication sources, including Kanidm OIDC login, have no resource
in this fork. Keep the existing CLI bootstrap/reconciliation with explicit
verification. The OAuth application resource configures Forgejo as an OAuth
provider for another application, not Forgejo's own external login provider.
Token minting also does not remove Forgejo's BasicAuth requirement or deliver
secrets to SOPS, Kubernetes or Woodpecker.

## Build and use locally

Run from this checkout:

```shell
make install-local
```

This builds version `1.6.1-ops.1` in the ignored `bin/mirror/` directory and writes
an isolated `bin/tofurc`. It does not change global OpenTofu settings or download
a similarly named registry provider. Use the resulting configuration explicitly:

```shell
export TF_CLI_CONFIG_FILE=/var/home/gordon/repos/github.com/elohmeier/terraform-provider-forgejo/bin/tofurc
```

In a separate configuration root:

```hcl
terraform {
  required_providers {
    forgejo = {
      source  = "registry.opentofu.org/elohmeier/forgejo"
      version = "1.6.1-ops.1"
    }
  }
}

provider "forgejo" {
  host = "https://forgejo.example.com"
  # Supply FORGEJO_API_TOKEN through the administrative secret-loading process.
}
```

Then run `tofu init` and `tofu validate`. The local mirror contains the current
platform's binary only. The local installation and schema validation were
verified with OpenTofu 1.12.6. Pin a reviewed fork commit and rebuild before use;
this development version is not a signed release or immutable registry artifact.
Inherited examples using `svalabs/forgejo` must use the fork source to exercise
these additions.

For existing resources, use import blocks, match live settings, inspect the
complete plan, and protect valuable repositories against destruction. Do not
combine first adoption with credential rotation or access-policy changes.

## Validation and maintenance

```shell
go test ./...
go vet ./...
golangci-lint run
make generate
python tools/test-acceptance.py
```

`make generate` uses OpenTofu to extract the compiled provider schema and then
runs the pinned documentation generator. It requires no production credentials,
registry publication or Terraform executable.

The acceptance runner requires Docker, Python 3, Go and OpenTofu. It creates a
fresh Forgejo 15.0.1 rootless container bound to `127.0.0.1:3000`, using SQLite and
temporary credentials held in process memory. It removes the container and its
anonymous volumes afterward. Port 3000 must be free. It never uses an external
Forgejo instance. Individual tests can be selected, for example:

```shell
python tools/test-acceptance.py ./internal/provider -run TestAccFork -v
```

The complete suite passed against this disposable server. Regressions cover
resource import, externally removed access grants, two consecutive no-change
plans, user-password preservation, pagination past 50 teams, organisation access
updates, OAuth secret rotation and access denial to a private repository outside
a token's allowlist. HTTP tests verify that 403/500 errors retain managed state
and that the API extension does not echo response bodies or follow redirects.

Token acceptance tests use one provider configuration because the in-process
protocol test harness does not isolate aliases. This limitation was reproduced
on unmodified upstream. Normal OpenTofu executions start separate provider
processes for aliases; use a compiled-provider integration test when changing
alias handling.

Upstream cherry-picks retain their authors and source commit IDs. Keep fork
changes separate from those imports to make future upstream rebases reviewable.
No upstream PRs or issue comments were posted as part of this work.
