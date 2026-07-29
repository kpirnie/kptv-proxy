// work/tmdb/tv.go
package tmdb

import (
	"fmt"
	"net/url"
)

// TVResult is a single entry from a TV series search.
type TVResult struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	FirstAirDate string `json:"first_air_date"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

type tvSearchResponse struct {
	Results []TVResult `json:"results"`
}

// TVDetails is the full detail response for a single series, including cast
// credits and external IDs via append_to_response.
type TVDetails struct {
	ID           int     `json:"id"`
	Overview     string  `json:"overview"`
	VoteAverage  float64 `json:"vote_average"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	Genres       []Genre `json:"genres"`
	Credits      struct {
		Cast []CastMember `json:"cast"`
	} `json:"credits"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
		TVDBID int    `json:"tvdb_id"`
	} `json:"external_ids"`
}

// SearchTV returns the best-guess match for a series name from TMDB's TV
// search, or nil when nothing matched. Unlike movies, a season/episode year
// rarely matches a show's own first-air year, so no year filter is applied.
func SearchTV(c *Client, name, year string) (*TVResult, error) {
	params := url.Values{"query": {name}}

	var out tvSearchResponse
	if err := c.get("/search/tv", params, &out); err != nil {
		return nil, err
	}
	if len(out.Results) == 0 {
		return nil, nil
	}
	return &out.Results[0], nil
}

// GetTVDetails fetches full series details, including cast and external IDs,
// for a TMDB TV series ID.
func GetTVDetails(c *Client, id int) (*TVDetails, error) {
	params := url.Values{"append_to_response": {"credits,external_ids"}}

	var out TVDetails
	if err := c.get(fmt.Sprintf("/tv/%d", id), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
