// work/tmdb/movies.go
package tmdb

import (
	"fmt"
	"net/url"
)

// Genre is a TMDB genre reference shared by movie and TV responses.
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// CastMember is a single credited cast entry.
type CastMember struct {
	Name      string `json:"name"`
	Character string `json:"character"`
}

// MovieResult is a single entry from a movie search.
type MovieResult struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	ReleaseDate  string `json:"release_date"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

type movieSearchResponse struct {
	Results []MovieResult `json:"results"`
}

// MovieDetails is the full detail response for a single movie, including
// cast credits via append_to_response.
type MovieDetails struct {
	ID           int     `json:"id"`
	IMDBID       string  `json:"imdb_id"`
	Overview     string  `json:"overview"`
	Tagline      string  `json:"tagline"`
	Runtime      int     `json:"runtime"`
	VoteAverage  float64 `json:"vote_average"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	Genres       []Genre `json:"genres"`
	Credits      struct {
		Cast []CastMember `json:"cast"`
	} `json:"credits"`
}

// SearchMovie returns the best-guess match for title (and year, if given)
// from TMDB's movie search, or nil when nothing matched.
func SearchMovie(c *Client, title, year string) (*MovieResult, error) {
	params := url.Values{"query": {title}}
	if year != "" {
		params.Set("year", year)
	}

	var out movieSearchResponse
	if err := c.get("/search/movie", params, &out); err != nil {
		return nil, err
	}
	if len(out.Results) == 0 {
		return nil, nil
	}
	return &out.Results[0], nil
}

// GetMovieDetails fetches full movie details, including cast, for a TMDB
// movie ID.
func GetMovieDetails(c *Client, id int) (*MovieDetails, error) {
	params := url.Values{"append_to_response": {"credits"}}

	var out MovieDetails
	if err := c.get(fmt.Sprintf("/movie/%d", id), params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
