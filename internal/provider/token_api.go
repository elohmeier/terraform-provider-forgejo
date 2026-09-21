package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
)

// tokenDetails extends the SDK token model with Forgejo 15 repository limits.
type tokenDetails struct {
	forgejo.AccessToken
	Repositories []struct {
		ID int64 `json:"id"`
	} `json:"repositories"`
}
type tokenRepositoryTarget struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

func (r *personalAccessTokenResource) createToken(ctx context.Context, user string, opts forgejo.CreateAccessTokenOption, repositoryIDs []int64, admin bool) (*tokenDetails, int, error) {
	payload := struct {
		forgejo.CreateAccessTokenOption
		Repositories []tokenRepositoryTarget `json:"repositories,omitempty"`
	}{CreateAccessTokenOption: opts}
	for _, id := range repositoryIDs {
		repo, _, err := r.client.GetRepoByID(id)
		if err != nil {
			return nil, 0, fmt.Errorf("cannot resolve repository ID %d", id)
		}
		payload.Repositories = append(payload.Repositories, tokenRepositoryTarget{Owner: repo.Owner.UserName, Name: repo.Name})
	}
	var token tokenDetails
	status, err := r.api.request(ctx, http.MethodPost, tokenPath(user, admin), payload, &token)
	return &token, status, err
}
func (r *personalAccessTokenResource) findToken(ctx context.Context, user string, id int64, admin bool) (*tokenDetails, error) {
	for page := 1; ; page++ {
		var tokens []tokenDetails
		status, err := r.api.request(ctx, http.MethodGet, fmt.Sprintf("%s?page=%d&limit=50", tokenPath(user, admin), page), nil, &tokens)
		if status == http.StatusNotFound {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, token := range tokens {
			if token.ID == id {
				return &token, nil
			}
		}
		if len(tokens) == 0 {
			return nil, nil
		}
	}
}

// Administrator endpoints added in Forgejo 16 support token authentication.
func tokenPath(user string, admin bool) string {
	prefix := "/users/"
	if admin {
		prefix = "/admin/users/"
	}
	return prefix + url.PathEscape(user) + "/tokens"
}
