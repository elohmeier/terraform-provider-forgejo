# Import metadata only; the token value cannot be recovered.
terraform import forgejo_personal_access_token.test_token username/123

# Use the administrator API on Forgejo 16+; set use_admin_api = true in configuration.
terraform import forgejo_personal_access_token.bot admin/bot-user/456
