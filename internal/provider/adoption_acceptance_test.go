package provider_test

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func forkClient(t *testing.T) *forgejo.Client {
	t.Helper()
	client, err := forgejo.NewClient(forgejoTestHost, forgejo.SetToken(os.Getenv("FORGEJO_API_TOKEN")))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

const forkAccessConfig = `
resource "forgejo_organization" "adopt" { name = "iac-adoption" }
resource "forgejo_team" "adopt" {
 organization = forgejo_organization.adopt.name
 name = "automation"
 includes_all_repositories = false
 units_map = { "repo.code" = "read", "repo.issues" = "write" }
}
resource "forgejo_repository" "adopt" {
 owner = forgejo_organization.adopt.name
 name = "managed"
 private = true
}
resource "forgejo_user" "adopt" {
 login = "iac-bot"
 email = "iac-bot@localhost.localdomain"
 password = "test-password"
}
resource "forgejo_team_member" "adopt" {
 team_id = forgejo_team.adopt.id
 user = forgejo_user.adopt.login
}
resource "forgejo_team_repository" "adopt" {
 team_id = forgejo_team.adopt.id
 repository_id = forgejo_repository.adopt.id
}
resource "forgejo_collaborator" "adopt" {
 repository_id = forgejo_repository.adopt.id
 user = forgejo_user.adopt.login
 permission = "write"
}
`

func TestAccForkAdoptionAndAccessDrift(t *testing.T) {
	var teamID int64
	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: providerConfig + forkAccessConfig, Check: func(s *terraform.State) error {
				var err error
				teamID, err = strconv.ParseInt(s.RootModule().Resources["forgejo_team.adopt"].Primary.ID, 10, 64)
				return err
			}},
			{ResourceName: "forgejo_organization.adopt", ImportState: true, ImportStateId: "iac-adoption", ImportStateVerify: true},
			{ResourceName: "forgejo_team_member.adopt", ImportStateVerifyIdentifierAttribute: "user", ImportState: true, ImportStateId: "iac-adoption/automation/iac-bot", ImportStateVerify: true},
			{ResourceName: "forgejo_team_repository.adopt", ImportStateVerifyIdentifierAttribute: "repository_id", ImportState: true, ImportStateId: "iac-adoption/automation/managed", ImportStateVerify: true},
			{ResourceName: "forgejo_collaborator.adopt", ImportStateVerifyIdentifierAttribute: "user", ImportState: true, ImportStateId: "iac-adoption/managed/iac-bot", ImportStateVerify: true},
			{Config: providerConfig + forkAccessConfig, PreConfig: func() {
				c := forkClient(t)
				if _, err := c.RemoveTeamMember(teamID, "iac-bot"); err != nil {
					t.Fatal(err)
				}
				if _, err := c.RemoveTeamRepository(teamID, "iac-adoption", "managed"); err != nil {
					t.Fatal(err)
				}
				if _, err := c.DeleteCollaborator("iac-adoption", "managed", "iac-bot"); err != nil {
					t.Fatal(err)
				}
			}, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("forgejo_team_member.adopt", plancheck.ResourceActionCreate),
				plancheck.ExpectResourceAction("forgejo_team_repository.adopt", plancheck.ResourceActionCreate),
				plancheck.ExpectResourceAction("forgejo_collaborator.adopt", plancheck.ResourceActionCreate),
			}}},
			{Config: providerConfig + forkAccessConfig, PlanOnly: true},
		},
	})
}

func TestAccForkOAuthApplication(t *testing.T) {
	config := func(name string) string {
		return providerConfig + fmt.Sprintf(`
resource "forgejo_oauth2_application" "test" {
 name = %q
 redirect_uris = ["https://woodpecker.example/authorize"]
 confidential_client = true
}`, name)
	}
	var secret, id string
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: config("iac-oauth"), Check: func(s *terraform.State) error {
			attrs := s.RootModule().Resources["forgejo_oauth2_application.test"].Primary.Attributes
			secret, id = attrs["client_secret"], attrs["id"]
			if secret == "" {
				return fmt.Errorf("creation did not return a client secret")
			}
			return nil
		}},
		{Config: config("iac-oauth-updated"), Check: func(s *terraform.State) error {
			if s.RootModule().Resources["forgejo_oauth2_application.test"].Primary.Attributes["client_secret"] == secret {
				return fmt.Errorf("client secret was not rotated by the API update")
			}
			return nil
		}},
		{ResourceName: "forgejo_oauth2_application.test", ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return id, nil }, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"client_secret"}},
		{Config: config("iac-oauth-updated"), PreConfig: func() {
			numericID, _ := strconv.ParseInt(id, 10, 64)
			if _, err := forkClient(t).DeleteOauth2(numericID); err != nil {
				t.Fatal(err)
			}
		}, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("forgejo_oauth2_application.test", plancheck.ResourceActionCreate)}}},
	}})
}

func TestAccForkRepositoryRestrictedToken(t *testing.T) {
	config := providerBasicAuthConfig + `
resource "forgejo_repository" "allowed" {
 name = "iac-token-allowed"
 private = true
}
resource "forgejo_repository" "denied" {
 name = "iac-token-denied"
 private = true
}
resource "forgejo_personal_access_token" "test" {
 provider = forgejo
 user = "tfadmin"
 name = "iac-restricted"
 scopes = ["read:repository"]
 repository_ids = [forgejo_repository.allowed.id]
}`
	var id string
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: config, Check: func(s *terraform.State) error {
			attrs := s.RootModule().Resources["forgejo_personal_access_token.test"].Primary.Attributes
			id = attrs["id"]
			c, err := forgejo.NewClient(forgejoTestHost, forgejo.SetToken(attrs["token"]))
			if err != nil {
				return err
			}
			if _, _, err := c.GetRepo("tfadmin", "iac-token-allowed"); err != nil {
				return fmt.Errorf("allowed repository inaccessible: %w", err)
			}
			if _, response, err := c.GetRepo("tfadmin", "iac-token-denied"); err == nil || response == nil || (response.StatusCode != 403 && response.StatusCode != 404) {
				return fmt.Errorf("repository restriction was not enforced")
			}
			return nil
		}},
		{Config: config, PlanOnly: true},
		{ResourceName: "forgejo_personal_access_token.test", ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return "tfadmin/" + id, nil }, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"token"}},
	}})
}

func TestAccForkPagination(t *testing.T) {
	config := providerConfig + `
resource "forgejo_organization" "pages" { name = "iac-pages" }
resource "forgejo_team" "pages" {
 count = 55
 organization = forgejo_organization.pages.name
 name = format("team-%02d",count.index)
 units_map = { "repo.code" = "read" }
}
data "forgejo_team" "last" {
 organization = forgejo_organization.pages.name
 name = "team-54"
 depends_on = [forgejo_team.pages]
}`
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: config, Check: resource.TestCheckResourceAttr("data.forgejo_team.last", "name", "team-54")},
		{Config: config, PlanOnly: true},
	}})
}

func TestAccForkImportUserWithoutPassword(t *testing.T) {
	const username = "iac-existing-user"
	const password = "keep-existing-password"
	config := providerConfig + `
resource "forgejo_user" "existing" {
 login = "iac-existing-user"
 email = "existing@localhost.localdomain"
 must_change_password = false
}
import {
 to = forgejo_user.existing
 id = "iac-existing-user"
}`
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: config, PreConfig: func() {
			flag := false
			if _, _, err := forkClient(t).AdminCreateUser(forgejo.CreateUserOption{Username: username, Email: "existing@localhost.localdomain", Password: password, MustChangePassword: &flag}); err != nil {
				t.Fatal(err)
			}
		}, Check: func(*terraform.State) error {
			c, err := forgejo.NewClient(forgejoTestHost, forgejo.SetBasicAuth(username, password))
			if err != nil {
				return err
			}
			if _, _, err = c.GetMyUserInfo(); err != nil {
				return fmt.Errorf("original password no longer authenticates: %w", err)
			}
			return nil
		}},
		{Config: config, PlanOnly: true},
	}})
}

func TestAccForkOrganizationVisibilityAndAccess(t *testing.T) {
	publicStep := resource.TestStep{}
	if os.Getenv("TF_ACC") != "" {
		version, _, err := forkClient(t).ServerVersion()
		if err != nil {
			t.Fatal(err)
		}
		if strings.SplitN(strings.TrimPrefix(version, "v"), "+", 2)[0] == "15.0.1" {
			publicStep.ExpectError = regexp.MustCompile("Forgejo ignored organization visibility change")
		} else {
			publicStep.Check = resource.TestCheckResourceAttr("forgejo_organization.visibility", "visibility", "public")
		}
	}

	config := func(visibility string, allow bool) string {
		return providerConfig + fmt.Sprintf(`
resource "forgejo_organization" "visibility" {
 name = "iac-visibility"
 full_name = "Preserved display name"
 description = "Preserved description"
 visibility = %q
 repo_admin_change_team_access = %t
}`, visibility, allow)
	}
	publicStep.Config = config("public", true)
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: config("public", true)},
		{Config: config("private", false), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("forgejo_organization.visibility", plancheck.ResourceActionUpdate)}}},
		{ResourceName: "forgejo_organization.visibility", ImportState: true, ImportStateId: "iac-visibility", ImportStateVerify: true},
		{Config: config("limited", true), Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("forgejo_organization.visibility", "full_name", "Preserved display name"), resource.TestCheckResourceAttr("forgejo_organization.visibility", "description", "Preserved description"))},
		publicStep,
		{Config: config("limited", true)},
	}})
}

func requireForgejo16(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		return
	}
	version, _, err := forkClient(t).ServerVersion()
	if err != nil {
		t.Fatal(err)
	}
	major, err := strconv.Atoi(strings.Split(strings.TrimPrefix(version, "v"), ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	if major < 16 {
		t.Skip("administrator token API requires Forgejo 16+")
	}
}

func TestAccForkAdminToken(t *testing.T) {
	requireForgejo16(t)
	config := providerConfig + `
resource "forgejo_user" "bot" {
 login = "iac-admin-token-bot"
 email = "iac-admin-token-bot@localhost.localdomain"
 password = "disposable-bot-password"
 must_change_password = false
}
resource "forgejo_repository" "allowed" {
 owner = forgejo_user.bot.login
 name = "allowed"
 private = true
}
resource "forgejo_repository" "denied" {
 owner = forgejo_user.bot.login
 name = "denied"
 private = true
}
resource "forgejo_personal_access_token" "bot" {
 user = forgejo_user.bot.login
 name = "automation"
 use_admin_api = true
 scopes = ["read:repository"]
 repository_ids = [forgejo_repository.allowed.id]
}`
	var id string
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: config, Check: func(s *terraform.State) error {
			attrs := s.RootModule().Resources["forgejo_personal_access_token.bot"].Primary.Attributes
			id = attrs["id"]
			if attrs["token"] == "" {
				return fmt.Errorf("administrator API did not return a token")
			}
			c, err := forgejo.NewClient(forgejoTestHost, forgejo.SetToken(attrs["token"]))
			if err != nil {
				return err
			}
			if _, _, err := c.GetRepo("iac-admin-token-bot", "allowed"); err != nil {
				return fmt.Errorf("bot token cannot read allowed repository")
			}
			if _, response, err := c.GetRepo("iac-admin-token-bot", "denied"); err == nil || response == nil || (response.StatusCode != 403 && response.StatusCode != 404) {
				return fmt.Errorf("bot token repository restriction not enforced")
			}
			return nil
		}},
		{Config: config, PlanOnly: true},
		{ResourceName: "forgejo_personal_access_token.bot", ImportState: true, ImportStateIdFunc: func(*terraform.State) (string, error) { return "admin/iac-admin-token-bot/" + id, nil }, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"token"}},
		{Config: config, PreConfig: func() {
			req, err := http.NewRequest(http.MethodDelete, forgejoTestHost+"/api/v1/admin/users/iac-admin-token-bot/tokens/"+id, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "token "+os.Getenv("FORGEJO_API_TOKEN"))
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("delete returned HTTP %d", response.StatusCode)
			}
		}, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("forgejo_personal_access_token.bot", plancheck.ResourceActionCreate)}}},
		{Config: config, PlanOnly: true},
	}})
}

func TestAccForkTokenChangeManagementAPI(t *testing.T) {
	requireForgejo16(t)
	config := func(admin bool) string {
		return providerBasicAuthConfig + fmt.Sprintf(`
resource "forgejo_personal_access_token" "switch" {
 user = "tfadmin"
 name = "iac-switch-api"
 scopes = ["read:repository"]
 use_admin_api = %t
}`, admin)
	}
	var id, token string
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, Steps: []resource.TestStep{
		{Config: config(false), Check: func(s *terraform.State) error {
			attrs := s.RootModule().Resources["forgejo_personal_access_token.switch"].Primary.Attributes
			id, token = attrs["id"], attrs["token"]
			return nil
		}},
		{Config: config(true), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("forgejo_personal_access_token.switch", plancheck.ResourceActionUpdate)}}, Check: func(s *terraform.State) error {
			attrs := s.RootModule().Resources["forgejo_personal_access_token.switch"].Primary.Attributes
			if attrs["id"] != id || attrs["token"] != token {
				return fmt.Errorf("switching management API rotated the token")
			}
			return nil
		}},
		{Config: config(true), PlanOnly: true},
	}})
}
