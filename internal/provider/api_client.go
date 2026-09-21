package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
)

// providerClient keeps SDK functionality and the small API surface not yet
// represented by the SDK under the same provider authentication configuration.
type providerClient struct {
	*forgejo.Client
	api *apiClient
}

func sdkClient(data any) (*forgejo.Client, bool) {
	c, ok := data.(*providerClient)
	if !ok {
		return nil, false
	}
	return c.Client, true
}

type apiClient struct {
	host, username, password, token string
	http                            *http.Client
}

func newAPIClient(host, username, password, token string) *apiClient {
	return &apiClient{host: strings.TrimRight(host, "/") + "/api/v1", username: username, password: password, token: token,
		http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

// request never includes response bodies in errors: token endpoints can return
// credentials. Redirects are not followed with authentication attached.
func (c *apiClient) request(ctx context.Context, method, path string, input, output any) (int, error) {
	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.host+path, &body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	} else {
		req.SetBasicAuth(c.username, c.password)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return res.StatusCode, fmt.Errorf("forgejo API returned HTTP %d", res.StatusCode)
	}
	if output != nil {
		if err := json.NewDecoder(res.Body).Decode(output); err != nil {
			return res.StatusCode, fmt.Errorf("invalid Forgejo API JSON response")
		}
	}
	return res.StatusCode, nil
}

func isNotFound(res *forgejo.Response, err error) bool {
	return err != nil && res != nil && res.StatusCode == http.StatusNotFound
}
