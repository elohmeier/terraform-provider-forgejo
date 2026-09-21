package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &teamRepositoryResource{}
	_ resource.ResourceWithConfigure   = &teamRepositoryResource{}
	_ resource.ResourceWithImportState = &teamRepositoryResource{}
)

type teamRepositoryResource struct {
	client *forgejo.Client
	api    *apiClient
}
type teamRepositoryResourceModel struct {
	TeamID       types.Int64 `tfsdk:"team_id"`
	RepositoryID types.Int64 `tfsdk:"repository_id"`
}

func NewTeamRepositoryResource() resource.Resource { return &teamRepositoryResource{} }
func (r *teamRepositoryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_repository"
}
func (r *teamRepositoryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "An explicit team-to-repository grant. Use a team with includes_all_repositories = false. Import using org/team/repo.", Attributes: map[string]schema.Attribute{
		"team_id":       schema.Int64Attribute{Description: "Numeric team ID. Changing this replaces the grant.", Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
		"repository_id": schema.Int64Attribute{Description: "Numeric repository ID. Changing this replaces the grant.", Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
	}}
}
func (r *teamRepositoryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*providerClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider configuration", fmt.Sprintf("Expected *providerClient, got %T", req.ProviderData))
		return
	}
	r.client, r.api = c.Client, c.api
}
func (r *teamRepositoryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data teamRepositoryResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	repo, diags := getRepositoryByID(ctx, r.client, data.RepositoryID.ValueInt64())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	team, _, err := r.client.GetTeam(data.TeamID.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read team", err.Error())
		return
	}
	if team.IncludesAllRepositories {
		resp.Diagnostics.AddError("Team grants access to all repositories", "Set includes_all_repositories = false before managing individual repository grants.")
		return
	}
	_, err = r.client.AddTeamRepository(data.TeamID.ValueInt64(), repo.Owner.UserName, repo.Name)
	if err != nil {
		resp.Diagnostics.AddError("Unable to add team repository", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
func (r *teamRepositoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data teamRepositoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	repo, result, err := r.client.GetRepoByID(data.RepositoryID.ValueInt64())
	if isNotFound(result, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read repository", err.Error())
		return
	}
	status, err := r.api.request(ctx, http.MethodGet, teamRepositoryPath(data.TeamID.ValueInt64(), repo.Owner.UserName, repo.Name), nil, nil)
	if status == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read team repository", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
func (r *teamRepositoryResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
}
func (r *teamRepositoryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data teamRepositoryResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	repo, result, err := r.client.GetRepoByID(data.RepositoryID.ValueInt64())
	if isNotFound(result, err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read repository", err.Error())
		return
	}
	result, err = r.client.RemoveTeamRepository(data.TeamID.ValueInt64(), repo.Owner.UserName, repo.Name)
	if err != nil && !isNotFound(result, err) {
		resp.Diagnostics.AddError("Unable to delete team repository", err.Error())
	}
}
func (r *teamRepositoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := importParts(req.ID, 3)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import identifier", "Expected org/team/repo.")
		return
	}
	team, diags := getOrgTeamByName(ctx, r.client, parts[0], parts[1])
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	repo, diags := getRepositoryByName(ctx, r.client, parts[0], parts[2])
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err = r.api.request(ctx, http.MethodGet, teamRepositoryPath(team.ID, parts[0], parts[2]), nil, nil)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import team repository", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &teamRepositoryResourceModel{TeamID: types.Int64Value(team.ID), RepositoryID: types.Int64Value(repo.ID)})...)
}
func teamRepositoryPath(id int64, owner, repo string) string {
	return fmt.Sprintf("/teams/%d/repos/%s/%s", id, url.PathEscape(owner), url.PathEscape(repo))
}
