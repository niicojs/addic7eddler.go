package main

import (
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/olebedev/config"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
)

// Show define a show
type Show struct {
	ID   string
	Name string
	URL  string
}

// Episode define an episode (like season 1, episode 2 of a show)
type Episode struct {
	Season    string
	Episode   string
	Lang      string
	Version   string
	Completed bool
	URL       string
}

// HistoryEntry represent each entry in download history
type HistoryEntry struct {
	Name string `json:"show"`
	URL  string `json:"url"`
}

var client *http.Client
var appconfig *config.Config
var history []HistoryEntry

func loadConfig() *config.Config {
	data, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		panic(err)
	}
	cfg, err := config.ParseYaml(string(data))
	if err != nil {
		panic(err)
	}
	return cfg
}

func loadHistory() []HistoryEntry {
	var loaded = make([]HistoryEntry, 0)
	data, err := ioutil.ReadFile("history.json")
	if err != nil {
		fmt.Println("No history?")
		return loaded
	}
	err = json.Unmarshal(data, &loaded)
	if err != nil {
		fmt.Println("Error loading history, invalid json")
		return loaded
	}
	return loaded
}

func saveHistory() {
	data, _ := json.MarshalIndent(history, "", "  ")
	ioutil.WriteFile("history.json", data, 0644)
}

func buildReq(url string) *http.Request {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8")
	req.Header.Add("Accept-Language", "fr,en-US;q=0.9,en;q=0.8")
	req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/62.0.3202.45 Safari/537.36")
	return req
}

func getAllShows() []Show {
	shows := make([]Show, 0)
	req := buildReq("http://www.addic7ed.com/shows.php")
	response, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	doc, _ := goquery.NewDocumentFromReader(response.Body)
	doc.Find("a").FilterFunction(func(i int, s *goquery.Selection) bool {
		href, exists := s.Attr("href")
		return exists && strings.Index(href, "/show/") == 0
	}).Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		name := s.Text()
		id := strings.Replace(href, "/show/", "", -1)
		url := "http://www.addic7ed.com" + href
		shows = append(shows, Show{ID: id, Name: name, URL: url})
	})
	return shows
}

func pickShows(shows []Show) []Show {
	exists := make(map[string]bool)
	filter, _ := appconfig.List("shows")
	for _, show := range filter {
		name := show.(string)
		exists[name] = true
	}
	filtered := make([]Show, 0)
	for _, show := range shows {
		if exists[show.Name] {
			filtered = append(filtered, show)
		}
	}
	return filtered
}

func filterWithHistory(episodes []Episode) []Episode {
	filtered := make([]Episode, 0)
	for _, episode := range episodes {
		found := false
		for _, done := range history {
			if episode.URL == done.URL {
				found = true
				break
			}
		}
		if !found {
			filtered = append(filtered, episode)
		}
	}
	return filtered
}

func download(show Show) int {
	fmt.Printf("Download from %s\n", show.Name)
	downloaded := 0

	// get latest season
	req := buildReq(show.URL)
	response, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	doc, _ := goquery.NewDocumentFromReader(response.Body)
	lastSeason := doc.Find("#sl button").Last().Text()
	fmt.Println("  season ", lastSeason)

	// get subtitles
	url := fmt.Sprintf("http://www.addic7ed.com/ajax_loadShow.php?show=%s&season=%s&langs=&hd=undefined&hi=undefined", show.ID, lastSeason)
	req = buildReq(url)
	response, err = client.Do(req)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	doc, _ = goquery.NewDocumentFromReader(response.Body)

	applang := appconfig.UString("language", "English")
	episodes := make([]Episode, 0)
	doc.Find("#season .completed").Each(func(i int, s *goquery.Selection) {
		tds := s.Find("td")
		url, _ := tds.Eq(9).Find("a").Attr("href")
		episode := Episode{
			Season:    tds.Eq(0).Text(),
			Episode:   tds.Eq(1).Text(),
			Lang:      tds.Eq(3).Text(),
			Version:   tds.Eq(4).Text(),
			Completed: tds.Eq(5).Text() == "Completed",
			URL:       "http://www.addic7ed.com" + url,
		}
		if episode.Completed && episode.Lang == applang {
			episodes = append(episodes, episode)
		}
	})
	episodes = filterWithHistory(episodes)

	// download
	for _, episode := range episodes {
		fmt.Println("  download episode", episode.Episode, "version", episode.Version)
		req = buildReq(episode.URL)
		req.Header.Add("Referer", "http://www.addic7ed.com/season/"+show.ID+"/"+episode.Season)
		response, _ = client.Do(req)
		filename := response.Header["Content-Disposition"][0]
		filename = filename[len("attachment; filename=")+1 : len(filename)-1]
		filename = filepath.Join(appconfig.UString("directory", "."), filename)
		output, _ := os.Create(filename)
		io.Copy(output, response.Body)
		output.Close()
		response.Body.Close()
		downloaded++

		history = append(history, HistoryEntry{Name: show.Name, URL: episode.URL})
	}

	return downloaded
}

func main() {
	fmt.Println("Addic7eddler - Go Version")
	appconfig = loadConfig()
	history = loadHistory()
	cookies, _ := cookiejar.New(nil)
	client = &http.Client{
		Jar: cookies,
	}
	shows := getAllShows()
	fmt.Printf("%d shows available\n", len(shows))
	shows = pickShows(shows)
	fmt.Printf("%d filtered shows\n", len(shows))

	downloaded := 0
	for _, show := range shows {
		downloaded += download(show)
	}

	fmt.Printf("%d file downloaded\n", downloaded)

	saveHistory()
}
