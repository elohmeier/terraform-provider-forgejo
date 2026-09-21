package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func importParts(id string, count int) ([]string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != count {
		return nil, fmt.Errorf("expected %d slash-separated components", count)
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("empty import component")
		}
	}
	return parts, nil
}

// ImportState adopts one membership and verifies it exists before saving state.
func (r *teamMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := importParts(req.ID, 3)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import identifier", "Expected org/team/username.")
		return
	}
	team, diags := getOrgTeamByName(ctx, r.client, parts[0], parts[1])
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(checkTeamMember(ctx, r.client, team.ID, parts[2])...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &teamMemberResourceModel{TeamID: types.Int64Value(team.ID), User: types.StringValue(parts[2])})...)
}

// ImportState adopts a direct collaborator grant, not inherited team access.
func (r *collaboratorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := importParts(req.ID, 3)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import identifier", "Expected owner/repository/username.")
		return
	}
	repo, diags := getRepositoryByName(ctx, r.client, parts[0], parts[1])
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	direct, _, err := r.client.IsCollaborator(parts[0], parts[1], parts[2])
	if err != nil {
		resp.Diagnostics.AddError("Unable to read collaborator", err.Error())
		return
	}
	if !direct {
		resp.Diagnostics.AddError("Collaborator not found", "Import requires an existing direct collaborator grant.")
		return
	}
	perms, _, err := r.client.CollaboratorPermission(parts[0], parts[1], parts[2])
	if err != nil {
		resp.Diagnostics.AddError("Unable to read collaborator permission", err.Error())
		return
	}
	data := collaboratorResourceModel{RepositoryID: types.Int64Value(repo.ID), User: types.StringValue(parts[2])}
	data.from(perms)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
