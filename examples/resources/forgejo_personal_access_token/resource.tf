terraform {
  required_providers {
    forgejo = {
      source  = "registry.opentofu.org/elohmeier/forgejo"
      version = "1.6.1-ops.2"
    }
  }
}

variable "test_password" { sensitive = true }

provider "forgejo" {
  host = "http://localhost:3000"
  // Forgejo 16+: supply an administrator FORGEJO_API_TOKEN with write:admin scope.
  // Include the scopes needed to read/manage other resources too.
  // Older servers: omit use_admin_api below and use BasicAuth credentials.
}

resource "forgejo_user" "test_user" {
  login    = "test_user"
  email    = "test_user@localhost.localdomain"
  password = var.test_password
}

resource "forgejo_personal_access_token" "test_token" {
  use_admin_api = true
  user          = forgejo_user.test_user.login
  name          = "test token"
  scopes = [
    "read:repository"
  ]
}
