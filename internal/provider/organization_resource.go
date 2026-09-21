package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource                = &organizationResource{}
	_ resource.ResourceWithConfigure   = &organizationResource{}
	_ resource.ResourceWithImportState = &organizationResource{}
)

// organizationResource is the resource implementation.
type organizationResource struct {
	client *forgejo.Client
	api    *apiClient
}

// organizationResourceModel maps the resource schema data.
// https://pkg.go.dev/codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3#Organization
type organizationResourceModel struct {
	ID                        types.Int64  `tfsdk:"id"`
	Name                      types.String `tfsdk:"name"`
	FullName                  types.String `tfsdk:"full_name"`
	AvatarURL                 types.String `tfsdk:"avatar_url"`
	Description               types.String `tfsdk:"description"`
	Website                   types.String `tfsdk:"website"`
	Location                  types.String `tfsdk:"location"`
	Visibility                types.String `tfsdk:"visibility"`
	RepoAdminChangeTeamAccess types.Bool   `tfsdk:"repo_admin_change_team_access"`
}

// from is a helper function to load an API struct into Terraform data model.
func (m *organizationResourceModel) from(o *forgejo.Organization) {
	if o == nil {
		return
	}

	m.ID = types.Int64Value(o.ID)
	m.Name = types.StringValue(o.UserName)
	m.FullName = types.StringValue(o.FullName)
	m.AvatarURL = types.StringValue(o.AvatarURL)
	m.Description = types.StringValue(o.Description)
	m.Website = types.StringValue(o.Website)
	m.Location = types.StringValue(o.Location)
	m.Visibility = types.StringValue(o.Visibility)

	// RepoAdminChangeTeamAccess intentionally omitted (write-only)
}

// to is a helper function to save Terraform data model into an API struct.
func (m *organizationResourceModel) to(o *forgejo.EditOrgOption) {
	if o == nil {
		return
	}

	o.FullName = m.FullName.ValueString()
	o.Description = m.Description.ValueString()
	o.Website = m.Website.ValueString()
	o.Location = m.Location.ValueString()
	o.Visibility = forgejo.VisibleType(m.Visibility.ValueString())
}

// Metadata returns the resource type name.
func (r *organizationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

// Schema defines the schema for the resource.
func (r *organizationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Forgejo organization resource.",

		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Description: "Numeric identifier of the organization.",
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Name of the organization. Changing this forces a new resource to be created.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"full_name": schema.StringAttribute{
				Description: "Full name of the organization.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"avatar_url": schema.StringAttribute{
				Description: "Avatar URL of the organization.",
				Computed:    true,
				// URLs may change outside of Terraform, so no UseStateForUnknown()
			},
			"description": schema.StringAttribute{
				Description: "Description of the organization.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"website": schema.StringAttribute{
				Description: "Website of the organization.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"location": schema.StringAttribute{
				Description: "Location of the organization.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"visibility": schema.StringAttribute{
				Description: "Visibility of the organization. Possible values are 'public' (default), 'limited', or 'private'.",
				Optional:    true,
				Computed:    true,
				// No static default value, because DEFAULT_ORG_VISIBILITY controls server setting
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf(
						"public",
						"limited",
						"private",
					),
				},
			},
			"repo_admin_change_team_access": schema.BoolAttribute{
				// The API exposes this field even though SDK v3.0.0 omits it.
				Description: "Whether repository admins can add and remove team access.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
		},
	}
}

// Configure adds the provider configured client to the resource.
func (r *organizationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	configured, ok := req.ProviderData.(*providerClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf(
				"Expected *providerClient, got: %T. Please report this issue to the provider developers.",
				req.ProviderData,
			),
		)

		return
	}

	r.client = configured.Client
	r.api = configured.api
}

// Create creates the resource and sets the initial Terraform state.
func (r *organizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer un(trace(ctx, "Create organization resource"))

	var data organizationResourceModel

	// Read Terraform plan data into model
	diags := req.Plan.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Create organization", map[string]any{
		"name":                          data.Name.ValueString(),
		"full_name":                     data.FullName.ValueString(),
		"description":                   data.Description.ValueString(),
		"website":                       data.Website.ValueString(),
		"location":                      data.Location.ValueString(),
		"visibility":                    data.Visibility.ValueString(),
		"repo_admin_change_team_access": data.RepoAdminChangeTeamAccess.ValueBool(),
	})

	// Generate API request body from plan
	opts := forgejo.CreateOrgOption{
		Name:                      data.Name.ValueString(),
		FullName:                  data.FullName.ValueString(),
		Description:               data.Description.ValueString(),
		Website:                   data.Website.ValueString(),
		Location:                  data.Location.ValueString(),
		Visibility:                forgejo.VisibleType(data.Visibility.ValueString()),
		RepoAdminChangeTeamAccess: data.RepoAdminChangeTeamAccess.ValueBool(),
	}

	// Validate API request body
	err := opts.Validate()
	if err != nil {
		resp.Diagnostics.AddError("Input validation error", err.Error())

		return
	}

	// Use Forgejo client to create new organization
	org, res, err := r.client.CreateOrg(opts)
	if err != nil {
		var msg string
		if res == nil {
			msg = fmt.Sprintf("Unknown error with nil response: %s", err)
		} else {
			tflog.Error(ctx, "Error", map[string]any{
				"status": res.Status,
			})

			switch res.StatusCode {
			case 403:
				msg = fmt.Sprintf(
					"Organization with name %s forbidden: %s",
					data.Name.String(),
					err,
				)
			case 422:
				msg = fmt.Sprintf("Input validation error: %s", err)
			default:
				msg = fmt.Sprintf(
					"Unknown error (status %d): %s",
					res.StatusCode,
					err,
				)
			}
		}
		resp.Diagnostics.AddError("Unable to create organization", msg)

		return
	}

	// Map response body to model
	data.from(org)

	// Save data into Terraform state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

// Read refreshes the Terraform state with the latest data.
func (r *organizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer un(trace(ctx, "Read organization resource"))

	var data organizationResourceModel

	// Read Terraform prior state data into the model
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	org, result, err := r.client.GetOrg(data.Name.ValueString())
	if isNotFound(result, err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization", err.Error())
		return
	}

	// Map response body to model
	data.from(org)
	if err := data.readAccessSetting(ctx, r.api); err != nil {
		resp.Diagnostics.AddError("Unable to read organization access setting", err.Error())
		return
	}

	// Save data into Terraform state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *organizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data organizationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts := struct {
		forgejo.EditOrgOption
		RepoAdminChangeTeamAccess bool `json:"repo_admin_change_team_access"`
	}{RepoAdminChangeTeamAccess: data.RepoAdminChangeTeamAccess.ValueBool()}
	data.to(&opts.EditOrgOption)
	// Send all settings together: omitted string fields in EditOrg are reset by
	// Forgejo. A separate PATCH for just the access flag would erase metadata.
	if _, err := r.api.request(ctx, http.MethodPatch, "/orgs/"+url.PathEscape(data.Name.ValueString()), opts, nil); err != nil {
		resp.Diagnostics.AddError("Unable to update organization", err.Error())
		return
	}
	org, diags := getOrganizationByName(ctx, r.client, data.Name.ValueString())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if org.Visibility != data.Visibility.ValueString() {
		resp.Diagnostics.AddError("Forgejo ignored organization visibility change", "Forgejo 15.0.1 cannot change an existing private/limited organisation back to public through either the organisation or admin-user API. Upgrade to a server version with this API bug fixed or change visibility through the web interface, then refresh. The provider will not replace the organisation or claim the change succeeded.")
		return
	}
	data.from(org)
	if err := data.readAccessSetting(ctx, r.api); err != nil {
		resp.Diagnostics.AddError("Unable to read organization access setting", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *organizationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	defer un(trace(ctx, "Delete organization resource"))

	var data organizationResourceModel

	// Read Terraform prior state data into the model
	diags := req.State.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "Delete organization", map[string]any{
		"name": data.Name.ValueString(),
	})

	// Use Forgejo client to delete existing organization
	res, err := r.client.DeleteOrg(data.Name.ValueString())
	if err != nil {
		var msg string
		if res == nil {
			msg = fmt.Sprintf("Unknown error with nil response: %s", err)
		} else {
			tflog.Error(ctx, "Error", map[string]any{
				"status": res.Status,
			})

			switch res.StatusCode {
			case 404:
				msg = fmt.Sprintf(
					"Organization with name %s not found: %s",
					data.Name.String(),
					err,
				)
			default:
				msg = fmt.Sprintf(
					"Unknown error (status %d): %s",
					res.StatusCode,
					err,
				)
			}
		}
		resp.Diagnostics.AddError("Unable to delete organization", msg)

		return
	}
}

// NewOrganizationResource is a helper function to simplify the provider implementation.
func NewOrganizationResource() resource.Resource {
	return &organizationResource{}
}

// ImportState adopts an organisation without creating or changing it.
func (r *organizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	org, diags := getOrganizationByName(ctx, r.client, req.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var data organizationResourceModel
	data.from(org)
	if err := data.readAccessSetting(ctx, r.api); err != nil {
		resp.Diagnostics.AddError("Unable to import organization access setting", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (m *organizationResourceModel) readAccessSetting(ctx context.Context, api *apiClient) error {
	var org struct {
		RepoAdminChangeTeamAccess bool `json:"repo_admin_change_team_access"`
	}
	if _, err := api.request(ctx, http.MethodGet, "/orgs/"+url.PathEscape(m.Name.ValueString()), nil, &org); err != nil {
		return err
	}
	m.RepoAdminChangeTeamAccess = types.BoolValue(org.RepoAdminChangeTeamAccess)
	return nil
}
