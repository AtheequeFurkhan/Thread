package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

// --- CONFIGURATION ---
const (
	DataDir     = "data"
	HistoryFile = "data/history.json"
	ReadmeTmpl  = "templates/README.tmpl"
)

// RSS Sources
var FeedSources = map[string]string{
	"Technology": "http://feeds.bbci.co.uk/news/technology/rss.xml",
	"AI & Tech":  "https://www.theverge.com/rss/index.xml",
	"Crypto":     "https://cointelegraph.com/rss",
	"World":      "http://feeds.bbci.co.uk/news/world/rss.xml",
	"Business":   "https://feeds.npr.org/1006/rss.xml",
}

// --- DATA STRUCTURES ---

type RSSItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

type RSSFeed struct {
	Items []RSSItem `xml:"channel>item"`
}

type Article struct {
	Title     string    `json:"title"`
	Link      string    `json:"link"`
	Category  string    `json:"category"`
	Summary   string    `json:"summary"`
	Published string    `json:"published"`
	FetchedAt time.Time `json:"fetched_at"`
	Sentiment string    `json:"sentiment"` 
}

type Store struct {
	Articles      []Article `json:"articles"`
	TotalCaptured int       `json:"total_captured"`
	LastUpdate    time.Time `json:"last_update"`
}

type ReadmeData struct {
	LastUpdate    string
	NextUpdate    string
	TotalCaptured int
	Categories    map[string][]Article
	LatestNews    []Article
	ArchiveLinks  []string
}

// --- MAIN LOGIC ---

func main() {
	ensureDir(DataDir)
	store := loadHistory()

	newArticles := []Article{}
	
	// 1. Fetch Feeds
	for category, url := range FeedSources {
		fmt.Printf("Fetching %s...\n", category)
		items, err := fetchRSS(url)
		if err != nil {
			fmt.Printf("Error fetching %s: %v\n", category, err)
			continue
		}

		for _, item := range items {
			// Deduplicate based on URL
			if !articleExists(store.Articles, item.Link) {
				article := Article{
					Title:     cleanText(item.Title),
					Link:      item.Link,
					Category:  category,
					Summary:   cleanText(item.Description),
					Published: item.PubDate,
					FetchedAt: time.Now(),
					Sentiment: analyzeSentiment(item.Title + " " + item.Description),
				}
				newArticles = append(newArticles, article)
				store.Articles = append([]Article{article}, store.Articles...) // Prepend
			}
		}
	}

	// 2. Limit History Size (Keep last 500 in json to avoid bloat, full archive is in MD files)
	if len(store.Articles) > 500 {
		store.Articles = store.Articles[:500]
	}

	// 3. Update Stats
	store.TotalCaptured += len(newArticles)
	store.LastUpdate = time.Now()

	if len(newArticles) == 0 {
		fmt.Println("No new news found.")
	} else {
		fmt.Printf("Found %d new articles.\n", len(newArticles))
		saveDailyLog(newArticles)
	}

	saveHistory(store)
	generateReadme(store)
}

// --- HELPERS ---

func fetchRSS(url string) ([]RSSItem, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var feed RSSFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, err
	}
	return feed.Items, nil
}

func articleExists(history []Article, link string) bool {
	for _, a := range history {
		if a.Link == link {
			return true
		}
	}
	return false
}

func loadHistory() Store {
	data, err := ioutil.ReadFile(HistoryFile)
	if err != nil {
		return Store{Articles: []Article{}, TotalCaptured: 0}
	}
	var store Store
	json.Unmarshal(data, &store)
	return store
}

func saveHistory(store Store) {
	data, _ := json.MarshalIndent(store, "", "  ")
	ioutil.WriteFile(HistoryFile, data, 0644)
}

// Simple keyword-based sentiment (Mock AI)
func analyzeSentiment(text string) string {
	text = strings.ToLower(text)
	pos := []string{"launch", "win", "record", "growth", "breakthrough", "profit", "gain", "success", "new"}
	neg := []string{"crash", "fail", "drop", "loss", "ban", "lawsuit", "attack", "dead", "crisis"}

	score := 0
	for _, w := range pos {
		if strings.Contains(text, w) {
			score++
		}
	}
	for _, w := range neg {
		if strings.Contains(text, w) {
			score--
		}
	}

	if score > 0 {
		return "🟢 Positive"
	} else if score < 0 {
		return "🔴 Negative"
	}
	return "⚪ Neutral"
}

func saveDailyLog(articles []Article) {
	dateStr := time.Now().Format("2006-01-02")
	filename := filepath.Join(DataDir, dateStr+".md")
	
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	for _, a := range articles {
		entry := fmt.Sprintf("### [%s](%s)\n**Category:** %s | **Sentiment:** %s | **Time:** %s\n\n> %s\n\n---\n", 
			a.Title, a.Link, a.Category, a.Sentiment, a.FetchedAt.Format("15:04"), a.Summary)
		f.WriteString(entry)
	}
}

func generateReadme(store Store) {
	tmplData, err := ioutil.ReadFile(ReadmeTmpl)
	if err != nil {
		fmt.Println("Template not found")
		return
	}

	// Organize by category
	cats := make(map[string][]Article)
	for _, a := range store.Articles {
		if len(cats[a.Category]) < 5 { // Top 5 per category
			cats[a.Category] = append(cats[a.Category], a)
		}
	}

	// Archive links
	files, _ := ioutil.ReadDir(DataDir)
	var archives []string
	for i := len(files) - 1; i >= 0; i-- { // Reverse order
		if strings.HasSuffix(files[i].Name(), ".md") {
			archives = append(archives, files[i].Name())
		}
		if len(archives) >= 7 { break } // Show last 7 days
	}

	data := ReadmeData{
		LastUpdate:    time.Now().Format(time.RFC850),
		NextUpdate:    time.Now().Add(15 * time.Minute).Format(time.RFC850),
		TotalCaptured: store.TotalCaptured,
		Categories:    cats,
		LatestNews:    store.Articles[:min(20, len(store.Articles))],
		ArchiveLinks:  archives,
	}

	t, err := template.New("readme").Parse(string(tmplData))
	if err != nil {
		panic(err)
	}

	f, _ := os.Create("README.md")
	defer f.Close()
	t.Execute(f, data)
}

func ensureDir(dirName string) {
	if _, err := os.Stat(dirName); os.IsNotExist(err) {
		os.Mkdir(dirName, 0755)
	}
}

func cleanText(t string) string {
	// Basic HTML tag stripping could go here
	return strings.TrimSpace(t)
}

func min(a, b int) int {
	if a < b { return a }
	return b
}