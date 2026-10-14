package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Catalog requests only run after an explicit search, never during player polling.
type Catalog interface {
	Search(context.Context, Credential, string, string, int) (SearchResults, error)
	PlayItem(context.Context, Credential, Device, SearchItem) error
}
type VolumeSetter interface {
	SetVolume(context.Context, Credential, Device, int) error
}
type SearchItem struct {
	Key, ID, Kind, Title, Artists, Album, Release string
	Duration                                      int64
}

func (i SearchItem) Length() string { return clock(i.Duration) }

var itemID = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)

func (i SearchItem) Valid() bool {
	return itemID.MatchString(i.ID) && (i.Kind == "track" || i.Kind == "album") && i.Title != "" && len(i.Title) <= 640 && len(i.Artists) <= 640 && len(i.Album) <= 640 && i.Duration >= 0 && i.Duration <= 604800000
}

type SearchResults struct {
	Query, Kind   string
	Offset, Total int
	Searched      bool
	Items         []SearchItem
}

func (r SearchResults) Previous() int { return max(0, r.Offset-10) }
func (r SearchResults) Next() int     { return r.Offset + 10 }
func (r SearchResults) HasNext() bool { return r.Offset+10 < r.Total && r.Offset+10 <= 1000 }
func validateSearch(q, kind string, offset int) error {
	if !utf8.ValidString(q) || utf8.RuneCountInString(q) > 160 || strings.TrimSpace(q) == "" || strings.ContainsAny(q, "\x00\r\n") {
		return errors.New("Enter a song, album or artist name (up to 160 characters)")
	}
	if (kind != "track" && kind != "album") || offset < 0 || offset > 1000 || offset%10 != 0 {
		return errors.New("Choose Songs or Albums and a valid results page")
	}
	return nil
}

type catalogItem struct {
	ID, Name, URI string
	Release       string `json:"release_date"`
	Artists       []struct{ Name string }
	Album         struct{ Name string }
	Duration      int64 `json:"duration_ms"`
	Playable      *bool `json:"is_playable"`
	Restrictions  map[string]string
}

func (c *Client) Search(ctx context.Context, credential Credential, q, kind string, offset int) (SearchResults, error) {
	q = strings.TrimSpace(q)
	result := SearchResults{Query: q, Kind: kind, Offset: offset, Searched: true}
	if e := validateSearch(q, kind, offset); e != nil {
		return result, e
	}
	var reply struct {
		Tracks struct {
			Items []catalogItem
			Total int
		}
		Albums struct {
			Items []catalogItem
			Total int
		}
	}
	params := url.Values{"q": {q}, "type": {kind}, "limit": {"10"}, "offset": {strconv.Itoa(offset)}}
	if e := c.request(ctx, "GET", c.API+"/search?"+params.Encode(), credential.AccessToken, "", nil, &reply); e != nil {
		return result, e
	}
	items, total := reply.Tracks.Items, reply.Tracks.Total
	if kind == "album" {
		items, total = reply.Albums.Items, reply.Albums.Total
	}
	if len(items) > 10 || total < 0 {
		return result, errors.New("Invalid Spotify search results")
	}
	result.Total = total
	for _, raw := range items {
		if raw.URI != "spotify:"+kind+":"+raw.ID || len(raw.Restrictions) > 0 || (raw.Playable != nil && !*raw.Playable) {
			continue
		}
		names := []string{}
		for _, a := range raw.Artists {
			names = append(names, a.Name)
		}
		item := SearchItem{ID: raw.ID, Kind: kind, Title: Text(raw.Name, 160), Artists: Text(strings.Join(names, ", "), 160), Album: Text(raw.Album.Name, 160), Release: Text(raw.Release, 20), Duration: raw.Duration}
		if item.Valid() {
			result.Items = append(result.Items, item)
		}
	}
	return result, nil
}
func (c *Client) PlayItem(ctx context.Context, credential Credential, d Device, item SearchItem) error {
	if !d.Valid() || d.Restricted || !item.Valid() {
		return ErrRestricted
	}
	uri := "spotify:" + item.Kind + ":" + item.ID
	var body []byte
	if item.Kind == "album" {
		body, _ = json.Marshal(map[string]any{"context_uri": uri, "offset": map[string]int{"position": 0}})
	} else {
		body, _ = json.Marshal(map[string]any{"uris": []string{uri}})
	}
	return c.request(ctx, "PUT", c.API+"/me/player/play?"+url.Values{"device_id": {d.ID}}.Encode(), credential.AccessToken, "application/json", body, nil)
}
func (c *Client) SetVolume(ctx context.Context, credential Credential, d Device, level int) error {
	if level < 0 || level > 100 || level%5 != 0 {
		return errors.New("Choose a volume from 0 to 100 in 5% steps")
	}
	if !d.Valid() || d.Restricted || !d.SupportsVolume || d.Volume == nil {
		return ErrRestricted
	}
	return c.request(ctx, "PUT", c.API+"/me/player/volume?"+url.Values{"device_id": {d.ID}, "volume_percent": {strconv.Itoa(level)}}.Encode(), credential.AccessToken, "", nil, nil)
}

type selection struct {
	item            SearchItem
	account, client string
	expires         time.Time
}

func (s *Service) Search(ctx context.Context, q, kind string, offset int) (SearchResults, error) {
	q = strings.TrimSpace(q)
	result := SearchResults{Query: q, Kind: kind, Offset: offset, Searched: true}
	if e := validateSearch(q, kind, offset); e != nil {
		return result, e
	}
	provider, ok := s.Provider.(Catalog)
	if !ok {
		return result, errors.New("Spotify search is unavailable")
	}
	if !s.work.TryLock() {
		return result, errors.New("Spotify is checking status; try searching again in a moment")
	}
	defer s.work.Unlock()
	s.mu.Lock()
	wait := time.Now().Before(s.next) || time.Since(s.lastSearch) < time.Second
	if !wait {
		s.lastSearch = time.Now()
	}
	s.mu.Unlock()
	if wait {
		return result, errors.New("Wait a moment before searching again")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c, e := s.credential(ctx)
	if e != nil {
		return result, e
	}
	result, e = provider.Search(ctx, c, q, kind, offset)
	if e != nil {
		var rate RateError
		if errors.As(e, &rate) {
			s.fail(e)
		}
		return result, e
	}
	if len(result.Items) > 10 {
		return SearchResults{}, errors.New("Invalid Spotify search results")
	}
	current, e := s.Repo.SpotifyCredential()
	if e != nil || current.Revision != c.Revision {
		return SearchResults{}, ErrChanged
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Bounded, short-lived choices prevent clients from supplying arbitrary URIs.
	if s.selections == nil {
		s.selections = map[string]selection{}
	}
	for key, choice := range s.selections {
		if time.Now().After(choice.expires) {
			delete(s.selections, key)
		}
	}
	if len(s.selections)+len(result.Items) > 120 {
		s.selections = map[string]selection{}
	}
	valid := []SearchItem{}
	for _, item := range result.Items {
		if !item.Valid() {
			continue
		}
		item.Key = Random()
		s.selections[item.Key] = selection{item: item, account: c.AccountID, client: c.ClientID, expires: time.Now().Add(10 * time.Minute)}
		valid = append(valid, item)
	}
	result.Items = valid
	return result, nil
}
func (s *Service) PlaySelection(ctx context.Context, id, key string) error {
	s.mu.Lock()
	choice, ok := s.selections[key]
	s.mu.Unlock()
	if !ok || time.Now().After(choice.expires) {
		return errors.New("Search again before playing this selection")
	}
	return s.command(ctx, id, "selection", nil, &choice)
}
