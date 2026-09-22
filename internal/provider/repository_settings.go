package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// SDK v3 omits this field from Repository even though the server returns it.
// Read it during adoption and refresh instead of inventing a false value.
func (r *repositoryResource) readRepositorySettings(ctx context.Context, data *repositoryResourceModel) error {
	var settings struct {
		FastForward *bool `json:"allow_fast_forward_only_merge"`
	}
	_, err := r.api.request(ctx, http.MethodGet, fmt.Sprintf("/repositories/%d", data.ID.ValueInt64()), nil, &settings)
	if err != nil {
		return err
	}
	if settings.FastForward != nil {
		data.AllowFastForwardOnly = types.BoolValue(*settings.FastForward)
	}
	return nil
}
