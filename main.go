package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
)

// --- Simple types to produce Torznab XML --- //

type RSS struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel Channel  `xml:"channel"`
}

type Channel struct {
	Title string `xml:"title"`
	Link  string `xml:"link"`
	Items []Item `xml:"item"`
}

type Item struct {
	Title     string      `xml:"title"`
	GUID      GUID        `xml:"guid"`
	Link      string      `xml:"link"`
	Size      int64       `xml:"size"`
	PubDate   string      `xml:"pubDate"`
	Enclosure Enclosure   `xml:"enclosure"`
	Attrs     []TorznabAt `xml:"torznab:attr"`
}

type GUID struct {
	IsPerma string `xml:"isPermaLink,attr"`
	Value   string `xml:",chardata"`
}

type Enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type TorznabAt struct {
	XMLName xml.Name `xml:"torznab:attr"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

// --- Simple in-memory cache --- //
type CacheEntry struct {
	Timestamp time.Time
	XML       []byte
}

var (
	cache      = map[string]CacheEntry{}
	cacheMutex sync.Mutex
	cacheTTL   = 30 * time.Second
)

// --- Main --- //
func main() {
	log.Println("SonarrSeaDexProxy v0.0.2")
	port := "6778"

	http.HandleFunc("/api", apiHandler)
	log.Printf("Starting Torznab proxy on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// apiHandler handles torznab-like requests from Sonarr/Prowlarr
func apiHandler(w http.ResponseWriter, r *http.Request) {
	// Basic logging
	log.Printf("%s %s %s\n", time.Now().Format(time.RFC3339), r.Method, r.URL.RawQuery)

	// Only support tvsearch for now
	t := r.URL.Query().Get("t")
	if t == "" {
		writeError(w, "missing t param", http.StatusBadRequest)
		return
	}

	switch t {
	case "tvsearch":
		handleTVSearch(w, r)
	case "caps":
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server title="SeaDex Proxy" />
  <limits default="100" max="100" />
  <searching>
    <search available="yes" supportedParams="q" />
    <tv-search available="yes" supportedParams="q,season,ep" />
  </searching>
  <categories>
    <category id="5000" name="TV">
      <subcat id="5070" name="TV/Anime" />
    </category>
  </categories>
</caps>
`))
	default:
		writeError(w, "unsupported t param", http.StatusBadRequest)
	}
}

func handleTVSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q") // free text
	season := r.URL.Query().Get("season")
	ep := r.URL.Query().Get("ep")
	apiKey := r.URL.Query().Get("apikey")
	// Optional: Sonarr/Prowlarr may include other IDs (check your instance)
	anidb := r.URL.Query().Get("anidb")
	tvdb := r.URL.Query().Get("tvdb")

	// create a cache key
	cacheKey := fmt.Sprintf("tvsearch|q=%s|s=%s|ep=%s|anidb=%s|tvdb=%s|key=%s",
		q, season, ep, anidb, tvdb, apiKey)

	// Cache check
	cacheMutex.Lock()
	if entry, ok := cache[cacheKey]; ok && time.Since(entry.Timestamp) < cacheTTL {
		cacheMutex.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		w.Write(entry.XML)
		return
	}
	cacheMutex.Unlock()

	// Determine lookup strategy
	lookup := LookupQuery{
		Query:  q,
		Season: season,
		Ep:     ep,
		AniDB:  anidb,
		TVDB:   tvdb,
	}

	if lookup.Query == "" && lookup.AniDB == "" && lookup.TVDB == "" {
		// RSS - ignored
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="1.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <atom:link rel="self" type="application/rss+xml" />
    <title>SeaDex Proxy</title>
	  <item>
		<title>Demon Slayer S03E01 - Test Release</title>
		<description>example description</description>
		<guid>SEADEX-TEST-001</guid>
		<comments>https://nyaa.si</comments>
		<link>https://example.com/download/test.torrent</link>
		<pubDate>Mon, 15 Sep 2025 12:00:00 +0000</pubDate>
		<enclosure url="https://example.com/download/test.torrent" />
		<size>1073741824</size>
		<category>5070</category>
		<category>127720</category>
        <category>2020</category>
        <torznab:attr name="category" value="5070" />
        <torznab:attr name="category" value="127720" />
        <torznab:attr name="category" value="2020" />
        <torznab:attr name="tag" value="freeleech" />
        <torznab:attr name="genre" value="" />
        <torznab:attr name="seeders" value="2218" />
        <torznab:attr name="grabs" value="29746" />
        <torznab:attr name="peers" value="2230" />
        <torznab:attr name="infohash" value="702e9c869571826c72a08dad60397916e9280ea8" />
        <torznab:attr name="downloadvolumefactor" value="0" />
        <torznab:attr name="uploadvolumefactor" value="1" />
	  </item>
  </channel>
</rss>
`))
		return
	}

	// Query SeaDex / releases.moe (placeholder)
	bestReleases, err := querySeaDex(lookup)
	if err != nil {
		log.Printf("error querying SeaDex: %v\n", err)
		writeError(w, "internal upstream error", http.StatusInternalServerError)
		return
	}

	log.Println("Retrieved ", len(bestReleases), " best releases")

	// Build Torznab XML items
	items := make([]Item, 0, len(bestReleases))
	for _, br := range bestReleases {
		nyaa, err := getNyaaInfo(br.DownloadURL)
		if err != nil {
			log.Printf("handleTVSearch: getGuid: %v\n", err)
			return
		}
		// Example: fill an Item with attributes from the SeaDex result
		// br should include: Title, DownloadURL, Size, PubDate, Seeders, InfoHash
		title := nyaa.Title
		if br.IsBest {
			title += " --SeaDexBest--"
		} else {
			title += " --SeaDex--"
		}
		item := Item{
			Title: title,
			GUID: GUID{
				IsPerma: "false",
				Value:   "seadex+" + nyaa.GUID,
			},
			Link:    br.DownloadURL,
			Size:    br.Size,
			PubDate: br.PubDate.Format(time.RFC1123Z),
			Enclosure: Enclosure{
				URL:    nyaa.DownloadURL,
				Length: br.Size,
				Type:   "application/x-bittorrent",
			},
			Attrs: []TorznabAt{
				{Name: "seeders", Value: strconv.Itoa(nyaa.Seeders)},
				{Name: "peers", Value: strconv.Itoa(nyaa.Leechers + nyaa.Seeders)},
				{Name: "infohash", Value: br.InfoHash},
			},
		}
		items = append(items, item)
	}

	rss := RSS{
		Version: "2.0",
		Channel: Channel{
			Title: "SeaDex",
			Link:  "http://localhost/",
			Items: items,
		},
	}

	// Marshal to XML using a small template so we can include torznab namespace
	xmlBytes, err := renderTorznabXML(rss)
	if err != nil {
		log.Printf("error rendering xml: %v\n", err)
		writeError(w, "internal xml error", http.StatusInternalServerError)
		return
	}

	// Update cache
	cacheMutex.Lock()
	cache[cacheKey] = CacheEntry{
		Timestamp: time.Now(),
		XML:       xmlBytes,
	}
	cacheMutex.Unlock()

	w.Header().Set("Content-Type", "application/xml")
	w.Write(xmlBytes)
}

// --- Helper: render XML with torznab namespace --- //
var torznabTemplate = template.Must(template.New("torznab").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <title>{{ .Channel.Title }}</title>
    <link>{{ .Channel.Link }}</link>
    {{- range .Channel.Items }}
    <item>
      <title>{{ .Title }}</title>
      <guid>{{ .GUID.Value }}</guid>
      <comments>{{ .Link }}</comments>
      <size>{{ .Size }}</size>
      <pubDate>{{ .PubDate }}</pubDate>
      <enclosure url="{{ .Enclosure.URL }}" length="{{ .Enclosure.Length }}" type="{{ .Enclosure.Type }}" />
      {{- range .Attrs }}
      <torznab:attr name="{{ .Name }}" value="{{ .Value }}" />
      {{- end }}
    </item>
    {{- end }}
  </channel>
</rss>`))

func renderTorznabXML(r RSS) ([]byte, error) {
	var out bytes.Buffer
	if err := torznabTemplate.Execute(&out, r); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type NyaaInfo struct {
	GUID        string
	Seeders     int
	Leechers    int
	Title       string
	DownloadURL string
}

func extractTitle(html []byte) (string, error) {
	re := regexp.MustCompile(`<h3\s+class="panel-title">\s*(.*?)\s*</h3>`)
	matches := re.FindSubmatch(html)
	if len(matches) < 2 {
		return "", fmt.Errorf("title not found")
	}
	return string(matches[1]), nil
}

func extractTorrentLink(html []byte) (string, error) {
	re := regexp.MustCompile(`/download/\d+\.torrent`)
	match := re.Find(html)
	if match == nil {
		return "", fmt.Errorf("torrent link not found")
	}
	return "https://nyaa.si" + string(match), nil
}

// getSeeders extracts the number from <span style="color: green;">NUMBER</span>
func extractSeeders(html []byte) (int, error) {
	re := regexp.MustCompile(`<span\s+style="color:\s*green;">\s*(\d+)\s*</span>`)
	matches := re.FindSubmatch(html)
	if len(matches) < 2 {
		return 0, fmt.Errorf("seeders not found")
	}
	return strconv.Atoi(string(matches[1]))
}

// getLeechers extracts the number from <span style="color: red;">NUMBER</span>
func extractLeechers(html []byte) (int, error) {
	re := regexp.MustCompile(`<span\s+style="color:\s*red;">\s*(\d+)\s*</span>`)
	matches := re.FindSubmatch(html)
	if len(matches) < 2 {
		return 0, fmt.Errorf("leechers not found")
	}
	return strconv.Atoi(string(matches[1]))
}

// getGuid fetches a Nyaa.si details page and extracts the magnet link.
// It returns the magnet URI or an error if none is found.
func getNyaaInfo(nyaaURL string) (NyaaInfo, error) {
	var out NyaaInfo
	resp, err := http.Get(nyaaURL)
	if err != nil {
		return out, fmt.Errorf("getGuid: fetch page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return out, fmt.Errorf("getGuid: status %d: %s", resp.StatusCode, string(b))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, fmt.Errorf("getGuid: read body: %w", err)
	}

	// Look for the magnet link (appears as <a href="magnet:?xt=urn:btih:...">)
	re := regexp.MustCompile(`href="(magnet:\?xt=urn:[^"]+)"`)
	matches := re.FindSubmatch(body)
	if matches == nil {
		return out, fmt.Errorf("getGuid: no magnet link found in %s", nyaaURL)
	}

	out.GUID = string(matches[1])
	out.Title, _ = extractTitle(body)
	out.Seeders, _ = extractSeeders(body)
	out.Leechers, _ = extractLeechers(body)
	out.DownloadURL, _ = extractTorrentLink(body)

	return out, nil
}

// --- LookupQuery & SeaDex result placeholder types --- //

type LookupQuery struct {
	Query  string
	Season string
	Ep     string
	AniDB  string
	TVDB   string
}

// BestRelease is the minimal data we need to produce a torznab item.
// Replace fields and parsing when connecting to releases.moe.
type BestRelease struct {
	Title       string
	DownloadURL string
	Size        int64
	PubDate     time.Time
	Seeders     int
	Peers       int
	InfoHash    string
	IsBest      bool
}

// querySeaDex implements a releases.moe lookup based on the LookupQuery
func querySeaDex(q LookupQuery) ([]BestRelease, error) {
	// Build request
	endpoint := "https://releases.moe/api/collections/entries/records"
	params := url.Values{}
	params.Set("page", "1")
	params.Set("perPage", "10") // fetch a few entries for title-fallback searches
	params.Set("skipTotal", "1")
	params.Set("expand", "trs")

	alID, err := resolveAniListID(q.Query)
	if err != nil {
		return nil, fmt.Errorf("querySeaDex: %v", err)
	}
	log.Printf("Resolved AniList ID: %d", alID)
	params.Set("filter", fmt.Sprintf("alID=%d", alID))

	fullURL := endpoint + "?" + params.Encode()
	log.Println("Querying SeaDex for: ", fullURL)
	resp, err := http.Get(fullURL)
	if err != nil {
		return nil, fmt.Errorf("querySeaDex: http get failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("querySeaDex: releases.moe returned %d: %s", resp.StatusCode, string(body))
	}

	// minimal structs to decode the pieces we need
	type sdFile struct {
		Length int64  `json:"length"`
		Name   string `json:"name"`
	}
	type sdTorrent struct {
		CollectionId string   `json:"collectionId"`
		ID           string   `json:"id"`
		InfoHash     string   `json:"infoHash"`
		URL          string   `json:"url"`
		Tracker      string   `json:"tracker"`
		IsBest       bool     `json:"isBest"`
		ReleaseGroup string   `json:"releaseGroup"`
		Files        []sdFile `json:"files"`
		Updated      string   `json:"updated"`
	}
	type sdItem struct {
		AlID   int `json:"alID,omitempty"`
		Expand struct {
			TRS []sdTorrent `json:"trs"`
		} `json:"expand"`
	}
	type sdResp struct {
		Items []sdItem `json:"items"`
	}

	var parsed sdResp
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("querySeaDex: json decode failed: %w", err)
	}

	var out []BestRelease

	// helper to convert release URL to absolute
	// makeAbsoluteURL := func(u, tracker string) string {
	// 	u = strings.TrimSpace(u)
	// 	if u == "" {
	// 		return ""
	// 	}
	// 	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
	// 		return u
	// 	}
	// 	// relative path -> assume releases.moe-hosted path
	// 	if strings.HasPrefix(u, "/") {
	// 		return "https://nyaa.si" + u
	// 	}
	// 	return "https://nyaa.si/" + u
	// }

	// helper to parse SeaDex time strings like "2025-02-21 18:45:12.181Z"
	parseSeaDexTime := func(s string) time.Time {
		if s == "" {
			return time.Now().UTC()
		}
		s2 := strings.TrimSpace(s)
		// replace first space with "T" so it becomes RFC3339-like
		if idx := strings.Index(s2, " "); idx >= 0 {
			s2 = s2[:idx] + "T" + s2[idx+1:]
		}
		if t, err := time.Parse(time.RFC3339Nano, s2); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, s2); err == nil {
			return t
		}
		return time.Now().UTC()
	}

	// small episode-extraction helper (tries multiple patterns)
	extractEpisodeNumber := func(name string) (int, bool) {
		// skip obvious OP/ED/NCED/NCOP markers when they appear directly before the number
		lower := strings.ToLower(name)
		if strings.Contains(lower, "nced") || strings.Contains(lower, "ncop") {
			// but don't return immediately; many episode files are " - 01 ..." and won't match "NCED..."
		}

		patterns := []string{
			`(?i)\s-\s*0*([0-9]{1,3})\b`,   // " - 01", " - 9"
			`(?i)episode\s*0*([0-9]{1,3})`, // "Episode 19"
			`(?i)\bE0*([0-9]{1,3})\b`,      // "E03"
			`(?i)\[0*([0-9]{1,3})\]`,       // "[01]"
		}

		for _, p := range patterns {
			re := regexp.MustCompile(p)
			m := re.FindStringSubmatch(name)
			if len(m) >= 2 {
				epStr := m[1]
				ep, err := strconv.Atoi(epStr)
				if err == nil {
					// Basic sanity: avoid matches that are part of "NCED 1" / "NCOP 1" strings
					// If the matched substring is immediately preceded by letters (no separator), skip.
					// (This check is conservative.)
					loc := re.FindStringIndex(name)
					if loc != nil && loc[0] > 0 {
						prev := name[:loc[0]]
						// if prev ends with letters and no separator, skip
						prev = strings.TrimSpace(prev)
						if prev != "" {
							last := prev[len(prev)-1]
							if ('a' <= last && last <= 'z') || ('A' <= last && last <= 'Z') {
								// likely part of a word; continue trying other patterns/fallback
								// but still accept in many normal cases — we only skip for obvious letter adjacency
							}
						}
					}
					return ep, true
				}
			}
		}
		return 0, false
	}

	// Iterate and produce BestRelease entries
	for _, item := range parsed.Items {
		for _, tr := range item.Expand.TRS {
			// if !tr.IsBest {
			// 	continue
			// }
			if !strings.HasPrefix(tr.URL, "https://nyaa.si") {
				// drop all non-nyaa
				continue
			}
			dl := tr.URL
			pubDate := parseSeaDexTime(tr.Updated)

			// If an episode was requested, match file-by-file
			if strings.TrimSpace(q.Ep) != "" {
				targetEp, err := strconv.Atoi(strings.TrimSpace(q.Ep))
				if err != nil {
					// invalid ep number - skip this torrent in episode mode
					continue
				}
				matched := 0
				for _, f := range tr.Files {
					if ep, ok := extractEpisodeNumber(f.Name); ok && ep == targetEp {
						br := BestRelease{
							Title:       f.Name,
							DownloadURL: dl,
							Size:        f.Length,
							PubDate:     pubDate,
							Seeders:     0, // releases.moe doesn't provide seeders in this endpoint
							Peers:       0,
							InfoHash:    tr.InfoHash,
							IsBest:      tr.IsBest,
						}
						out = append(out, br)
						matched++
					}
				}
				// fallback: if no individual file matched, return the seasonpack/torrent as a candidate
				if matched == 0 {
					sum := int64(0)
					for _, f := range tr.Files {
						sum += f.Length
					}
					title := fmt.Sprintf("%s - %s - ep %s", tr.ReleaseGroup, defaultIfEmpty(q.Query, "Show"), q.Ep)
					br := BestRelease{
						Title:       title,
						DownloadURL: dl,
						Size:        sum,
						PubDate:     pubDate,
						Seeders:     0,
						Peers:       0,
						InfoHash:    tr.InfoHash,
						IsBest:      tr.IsBest,
					}
					out = append(out, br)
				}
				// we continue to next torrent
				continue
			}

			// If a season was requested (but no episode), surface the pack/torrent
			if strings.TrimSpace(q.Season) != "" {
				sum := int64(0)
				for _, f := range tr.Files {
					sum += f.Length
				}
				seasonInt, _ := strconv.Atoi(q.Season)
				title := fmt.Sprintf("[%s] %s Season %02d", tr.ReleaseGroup, defaultIfEmpty(q.Query, "Show"), seasonInt)
				br := BestRelease{
					Title:       title,
					DownloadURL: dl,
					Size:        sum,
					PubDate:     pubDate,
					Seeders:     0,
					Peers:       0,
					InfoHash:    tr.InfoHash,
					IsBest:      tr.IsBest,
				}
				out = append(out, br)
				continue
			}

			// No season/episode requested: return a representative item for the torrent
			sum := int64(0)
			repTitle := tr.ReleaseGroup
			if len(tr.Files) > 0 {
				repTitle = tr.Files[0].Name
			}
			for _, f := range tr.Files {
				sum += f.Length
			}
			br := BestRelease{
				Title:       repTitle,
				DownloadURL: dl,
				Size:        sum,
				PubDate:     pubDate,
				Seeders:     0,
				Peers:       0,
				InfoHash:    tr.InfoHash,
				IsBest:      tr.IsBest,
			}
			if !strings.HasPrefix(br.DownloadURL, "https://releases.moe") {
				out = append(out, br)
			}
		}
	}

	return out, nil
}

// resolveAniListID queries AniList's GraphQL API for the given title search string.
// It returns the AniList ID of the first matching result, or 0 if none are found.
func resolveAniListID(query string) (int, error) {
	endpoint := "https://graphql.anilist.co"

	graphqlQuery := `
		query ($search: String) {
			Media(search: $search, type: ANIME) {
				id
				title {
					romaji
					english
					native
				}
			}
		}
	`

	payload := map[string]interface{}{
		"query": graphqlQuery,
		"variables": map[string]interface{}{
			"search": query,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("resolveAniListID: marshal: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(body))
	if err != nil {
		return 0, fmt.Errorf("resolveAniListID: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("resolveAniListID: http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("resolveAniListID: status %d: %s", resp.StatusCode, string(b))
	}

	// Minimal response struct
	type aniResp struct {
		Data struct {
			Media struct {
				ID    int `json:"id"`
				Title struct {
					Romaji  string `json:"romaji"`
					English string `json:"english"`
					Native  string `json:"native"`
				} `json:"title"`
			} `json:"Media"`
		} `json:"data"`
	}

	var parsed aniResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("resolveAniListID: decode: %w", err)
	}

	if parsed.Data.Media.ID == 0 {
		return 0, nil // no match
	}

	return parsed.Data.Media.ID, nil
}

func defaultIfEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.WriteHeader(code)
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "%s\n", msg)
}
