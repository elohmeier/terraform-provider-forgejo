provider "forgejo" {
  host = "https://forgejo.example.com"
  # FORGEJO_API_TOKEN must belong to the application owner.
}

resource "forgejo_oauth2_application" "woodpecker" {
  name                = "Woodpecker CI"
  redirect_uris       = ["https://woodpecker.example.com/authorize"]
  confidential_client = true
  # Every API update regenerates client_secret. Deliver the new value to the
  # consumer in the same rollout. Encrypt state and never print the secret.
}
