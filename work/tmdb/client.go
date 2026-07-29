// work/tmdb/client.go
package tmdb

import (
	"encoding/json"
	"fmt"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"net/http"
	"net/url"
)

// Client wraps HTTP operations for the TMDB API, injecting the API key on
// every request.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

// NewClient constructs a Client authenticated with the given TMDB API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: constants.Internal.TMDBTimeout},
	}
}

// get performs an authenticated GET request to the given TMDB API path and
// decodes the JSON response into dest.
func (c *Client) get(path string, params url.Values, dest any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", c.apiKey)

	full := constants.Internal.TMDBBaseUrl + path + "?" + params.Encode()

	resp, err := c.httpClient.Get(full)
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d", path, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode GET response from %s: %w", path, err)
	}

	logger.Debug("{tmdb/client - get} GET %s OK", path)
	return nil
}
