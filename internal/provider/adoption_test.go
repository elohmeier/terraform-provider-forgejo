package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func mockForgejo(t *testing.T, handler http.HandlerFunc) (*forgejo.Client, *apiClient) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/version" {
			_, _ = w.Write([]byte(`{"version":"15.0.1"}`))
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := forgejo.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client, newAPIClient(server.URL, "", "", "test-token")
}

func TestPaginatedLookupsPastShortPage(t *testing.T) {
	for _, kind := range []string{"organization", "team", "token"} {
		t.Run(kind, func(t *testing.T) {
			pages := []string{}
			client, _ := mockForgejo(t, func(w http.ResponseWriter, r *http.Request) {
				page := r.URL.Query().Get("page")
				pages = append(pages, page)
				// The server caps limit=50 to one item. A short page is not the end.
				id, name := 1, "earlier"
				if page == "2" {
					id, name = 42, "wanted"
				}
				if page == "3" {
					_, _ = w.Write([]byte(`[]`))
					return
				}
				if err := json.NewEncoder(w).Encode([]map[string]any{{"id": id, "name": name, "username": name}}); err != nil {
					t.Error(err)
				}
			})
			ctx := context.Background()
			switch kind {
			case "organization":
				value, diags := getOrganizationByID(ctx, client, 42)
				if diags.HasError() || value == nil || value.ID != 42 {
					t.Fatalf("lookup failed: %v", diags)
				}
			case "team":
				value, diags := getOrgTeamByName(ctx, client, "org", "wanted")
				if diags.HasError() || value == nil || value.ID != 42 {
					t.Fatalf("lookup failed: %v", diags)
				}
			case "token":
				value, diags := getPersonalAccessToken(ctx, client, "user", "wanted")
				if diags.HasError() || value == nil || value.ID != 42 {
					t.Fatalf("lookup failed: %v", diags)
				}
			}
			if strings.Join(pages, ",") != "1,2" {
				t.Fatalf("expected both pages, got %v", pages)
			}
		})
	}
}

func TestManagedResourcesDistinguishMissingFromForbidden(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError} {
		for _, kind := range []string{"organization", "team", "member", "team_repository", "collaborator", "oauth", "token", "admin_token"} {
			t.Run(fmt.Sprintf("%s/%d", kind, status), func(t *testing.T) {
				client, api := mockForgejo(t, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"test"}`))
				})
				var r resource.Resource
				var model any
				switch kind {
				case "organization":
					r = &organizationResource{client: client}
					model = &organizationResourceModel{Name: types.StringValue("org")}
				case "team":
					r = &teamResource{client: client}
					model = &teamResourceModel{ID: types.Int64Value(1), UnitsMap: types.MapNull(types.StringType)}
				case "member":
					r = &teamMemberResource{client: client}
					model = &teamMemberResourceModel{TeamID: types.Int64Value(1), User: types.StringValue("bot")}
				case "team_repository":
					r = &teamRepositoryResource{client: client, api: api}
					model = &teamRepositoryResourceModel{TeamID: types.Int64Value(1), RepositoryID: types.Int64Value(2)}
				case "collaborator":
					r = &collaboratorResource{client: client}
					model = &collaboratorResourceModel{RepositoryID: types.Int64Value(2), User: types.StringValue("bot")}
				case "oauth":
					r = &oauth2ApplicationResource{client: client}
					model = &oauth2ApplicationModel{ID: types.Int64Value(1), RedirectURIs: types.SetNull(types.StringType)}
				case "token", "admin_token":
					r = &personalAccessTokenResource{client: client, api: api}
					model = &personalAccessTokenResourceModel{UseAdminAPI: types.BoolValue(kind == "admin_token"), User: types.StringValue("bot"), ID: types.Int64Value(1), Scopes: types.SetNull(types.StringType), RepositoryIDs: types.SetNull(types.Int64Type)}
				}
				ctx := context.Background()
				var schema resource.SchemaResponse
				r.Schema(ctx, resource.SchemaRequest{}, &schema)
				state := tfsdk.State{Schema: schema.Schema}
				if diags := state.Set(ctx, model); diags.HasError() {
					t.Fatal(diags)
				}
				response := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &response)
				if status == http.StatusNotFound {
					if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
						t.Fatalf("missing resource was not removed: %v", response.Diagnostics)
					}
				} else if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
					t.Fatal("access/server error must preserve state and fail")
				}
			})
		}
	}
}

func TestAPIClientDoesNotLeakErrorBodiesOrFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			_, api := mockForgejo(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/credential-sink")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"secret":"do-not-log"}`))
			})
			code, err := api.request(context.Background(), http.MethodPost, "/tokens", map[string]string{"secret": "do-not-log"}, nil)
			if code != status || err == nil || strings.Contains(err.Error(), "do-not-log") || calls != 1 {
				t.Fatalf("unexpected error handling: status=%d calls=%d error=%v", code, calls, err)
			}
		})
	}
}
