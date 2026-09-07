package main

import (
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	torrentlog "github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
	tea "github.com/charmbracelet/bubbletea"
)

type result struct {
	name, size, magnet, category, date string
	source, sourceURL, check, flag     string
	seeds, leechers                    int
}
type model struct {
	client                *torrent.Client
	query, status         string
	rows                  []result
	cursor, width, height int
	download              *torrent.Torrent
	meter                 transferMeter
	searching             bool
}

func search(q string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequest("GET", "https://knaben.org/search/"+url.PathEscape(q)+"/0/1/seeders", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("search returned %s", resp.Status)
		}
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			return err
		}
		var rows []result
		seen := make(map[string]bool)
		doc.Find("tr[data-id]").EachWithBreak(func(_ int, tr *goquery.Selection) bool {
			a := tr.Find(`a[href^="magnet:"]`).First()
			magnet, ok := a.Attr("href")
			if !ok {
				return true
			}
			u, err := url.Parse(magnet)
			if err != nil {
				return true
			}
			hash := strings.ToLower(u.Query().Get("xt"))
			if hash == "" || seen[hash] {
				return true
			}
			seen[hash] = true
			td := tr.Find("td")
			text := func(i int) string { return strings.TrimSpace(td.Eq(i).Text()) }
			number := func(i int) int { n, _ := strconv.Atoi(strings.ReplaceAll(text(i), ",", "")); return n }
			flag := strings.TrimSpace(td.Eq(1).Find(".badge").Last().Text())
			rows = append(rows, result{
				strings.TrimSpace(a.AttrOr("title", text(1))), text(2), magnet,
				strings.Join(strings.Fields(text(0)), " "), text(3), text(6),
				td.Eq(6).Find("a").First().AttrOr("href", ""),
				tr.Find("[data-bs-original-title]").First().AttrOr("data-bs-original-title", ""),
				flag, number(4), number(5),
			})
			return len(rows) < 30
		})
		return rows
	}
}

func (m model) Init() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return t })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = x.Width, x.Height
	case []result:
		m.rows, m.cursor, m.status, m.searching = x, 0, "", false
		if len(x) == 0 {
			m.status = "No results. Try a different title or year."
		}
	case error:
		m.status, m.searching = x.Error(), false
	case time.Time:
		if t := m.download; t != nil && t.Info() != nil {
			if m.meter.sampled.IsZero() {
				t.DownloadAll()
			}
			stats := t.Stats()
			m.meter.sample(x, stats.BytesReadUsefulData.Int64())
		}
		return m, m.Init()
	case tea.KeyMsg:
		if x.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if len(m.rows) == 0 {
			return m.searchKey(x)
		}
		switch x.String() {
		case "q":
			return m, tea.Quit
		case "esc", "/":
			m.rows, m.cursor, m.query, m.status = nil, 0, "", ""
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(len(m.rows)-1, m.cursor+1)
		case "o":
			target := m.rows[m.cursor].sourceURL
			return m, func() tea.Msg {
				u, err := url.Parse(target)
				if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
					return fmt.Errorf("no source page available")
				}
				return exec.Command("open", target).Run()
			}
		case "enter":
			t, err := m.client.AddMagnet(m.rows[m.cursor].magnet)
			if err != nil {
				m.status = err.Error()
				break
			}
			m.status = ""
			if t != m.download {
				if m.download != nil {
					m.download.Drop()
				}
				m.download, m.meter = t, transferMeter{}
			}
		}
	}
	return m, nil
}

func (m model) searchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		return m, nil
	}
	switch k.Type {
	case tea.KeyEnter:
		if strings.TrimSpace(m.query) != "" {
			m.status, m.searching = "searching…", true
			return m, search(m.query)
		}
	case tea.KeyBackspace, tea.KeyDelete:
		r := []rune(m.query)
		if len(r) > 0 {
			m.query = string(r[:len(r)-1])
		}
	case tea.KeyRunes:
		m.query += string(k.Runes)
	case tea.KeySpace:
		m.query += " "
	}
	return m, nil
}

func configureLogs(w io.Writer) {
	handler := torrentlog.DefaultHandler
	handler.W = w
	torrentlog.Default.SetHandlers(handler)
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelWarn})))
	stdlog.SetOutput(w)
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	logDir := filepath.Join(cache, "spate")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "spate.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	configureLogs(logFile)
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = filepath.Join(home, "Downloads", "spate")
	cfg.Logger, cfg.Slogger = torrentlog.Default, slog.Default()
	c, err := torrent.NewClient(cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err = tea.NewProgram(model{client: c, width: 80, height: 24}, tea.WithAltScreen()).Run(); err != nil {
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "spate:", err)
		os.Exit(1)
	}
}
