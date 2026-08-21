package main

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/anacrolix/torrent"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type result struct {
	name, size, magnet                             string
	category, date, source, sourceURL, check, flag string
	seeds, leechers                                int
}
type found struct {
	rows []result
	err  error
}
type tick time.Time
type model struct {
	client         *torrent.Client
	query, status  string
	rows           []result
	cursor, height int
	download       *torrent.Torrent
	last, rate     int64
}

var (
	blue = lipgloss.Color("#7DCFFF")
	dim  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	hot  = lipgloss.NewStyle().Bold(true).Foreground(blue)
	pick = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E0F2FE")).Background(lipgloss.Color("#1E3A5F"))
)

func search(q string) tea.Cmd {
	return func() tea.Msg {
		u := "https://knaben.org/search/" + url.PathEscape(q) + "/0/1/seeders"
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return found{err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return found{err: fmt.Errorf("search returned %s", resp.Status)}
		}
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			return found{err: err}
		}
		var rows []result
		doc.Find("tr[data-id]").EachWithBreak(func(_ int, tr *goquery.Selection) bool {
			a := tr.Find(`a[href^="magnet:"]`).First()
			magnet, ok := a.Attr("href")
			if !ok {
				return true
			}
			td := tr.Find("td")
			name := html.UnescapeString(strings.TrimSpace(a.AttrOr("title", td.Eq(1).Text())))
			seeds, _ := strconv.Atoi(strings.TrimSpace(td.Eq(4).Text()))
			leechers, _ := strconv.Atoi(strings.TrimSpace(td.Eq(5).Text()))
			category := strings.Join(strings.Fields(td.Eq(0).Text()), " ")
			check := tr.Find("[data-bs-original-title]").First().AttrOr("data-bs-original-title", "")
			flag := ""
			td.Eq(1).Find(".badge").EachWithBreak(func(_ int, badge *goquery.Selection) bool {
				flag = strings.TrimSpace(badge.Text())
				return flag == ""
			})
			rows = append(rows, result{
				name, strings.TrimSpace(td.Eq(2).Text()), magnet,
				category, strings.TrimSpace(td.Eq(3).Text()), strings.TrimSpace(td.Eq(6).Text()),
				td.Eq(6).Find("a").First().AttrOr("href", ""), check, flag,
				seeds, leechers,
			})
			return len(rows) < 10
		})
		return found{rows: rows}
	}
}

func (m model) Init() tea.Cmd { return nil }
func pulse() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tick(t) })
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = x.Height
	case tea.KeyMsg:
		switch x.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.rows, m.cursor, m.status = nil, 0, ""
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.rows) {
				m.cursor++
			}
		case "o":
			if len(m.rows) > 0 {
				page, err := url.Parse(m.rows[m.cursor].sourceURL)
				if err == nil && (page.Scheme == "http" || page.Scheme == "https") {
					_ = exec.Command("open", page.String()).Start()
				}
			}
		case "enter":
			if len(m.rows) == 0 && strings.TrimSpace(m.query) != "" {
				m.status = "searching…"
				return m, search(m.query)
			}
			if len(m.rows) > 0 {
				t, err := m.client.AddMagnet(m.rows[m.cursor].magnet)
				if err != nil {
					m.status = err.Error()
				} else {
					go func() { <-t.GotInfo(); t.DownloadAll() }()
					m.download, m.last = t, 0
					m.status = "finding metadata…"
					return m, pulse()
				}
			}
		case "backspace":
			if len(m.rows) == 0 && len(m.query) > 0 {
				m.query = m.query[:len(m.query)-1]
			}
		default:
			if len(m.rows) == 0 {
				m.query += string(x.Runes)
			}
		}
	case found:
		m.rows, m.cursor = x.rows, 0
		if x.err != nil {
			m.status = x.err.Error()
		} else {
			m.status = fmt.Sprintf("%d results", len(x.rows))
		}
	case tick:
		if m.download != nil {
			done := m.download.BytesCompleted()
			m.rate, m.last = (done-m.last)*2, done
			if m.download.Length() == 0 {
				m.status = "finding metadata…"
				return m, pulse()
			}
			if m.download.BytesMissing() > 0 {
				m.status = "downloading"
				return m, pulse()
			}
			m.status, m.rate = "complete", 0
		}
	}
	return m, nil
}

func bytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	for _, unit := range units {
		v /= 1024
		if v < 1024 {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
	}
	return fmt.Sprintf("%.1f PB", v/1024)
}

func (m model) downloadView() string {
	total, done := m.download.Length(), m.download.BytesCompleted()
	if total == 0 {
		return hot.Render("○ " + m.status)
	}
	pct := min(1, float64(done)/float64(total))
	filled := int(pct * 32)
	bar := hot.Render(strings.Repeat("▓", filled)) + dim.Render(strings.Repeat("░", 32-filled))
	peers := m.download.Stats().ActivePeers
	return fmt.Sprintf("%s\n%s  %5.1f%%\n%s  %s  %s/s  %d peers",
		hot.Render(m.download.Name()), bar, pct*100,
		bytes(done), dim.Render("of "+bytes(total)), bytes(max(0, m.rate)), peers)
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func detailView(r result) string {
	meta := strings.Trim(strings.Join([]string{r.category, r.source, r.date}, "  •  "), "  •")
	status := r.check
	if r.flag != "" {
		status += "  •  ⚠ " + r.flag
	}
	return hot.Render("selected") + "\n" + shorten(r.name, 76) + "\n" +
		dim.Render(meta) + "\n" + dim.Render(status)
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(hot.Render("spate") + "\n\n")
	if len(m.rows) == 0 {
		b.WriteString("  ❯ " + m.query + "█\n\n" + dim.Render("enter search  •  ctrl+c quit"))
	} else {
		b.WriteString(dim.Render("     NAME                                           SIZE  SEEDS  ↓NOW") + "\n")
		visible := min(10, max(3, m.height-16))
		start := max(0, min(m.cursor-visible+1, len(m.rows)-visible))
		end := min(len(m.rows), start+visible)
		for i, r := range m.rows[start:end] {
			i += start
			line := fmt.Sprintf(" %2d  %-46.46s %8.8s %6d %5d ", i+1, r.name, r.size, r.seeds, r.leechers)
			if i == m.cursor {
				line = pick.Render("▍" + line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + dim.Render("↑↓ choose  •  enter download  •  o description  •  esc search again"))
		b.WriteString("\n\n" + detailView(m.rows[m.cursor]))
	}
	if m.download != nil {
		b.WriteString("\n\n" + m.downloadView())
	} else if m.status != "" && len(m.rows) == 0 {
		b.WriteString("\n\n" + hot.Render(m.status))
	}
	return lipgloss.NewStyle().Margin(2, 4).Render(b.String())
}

func main() {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Downloads", "spate")
	_ = os.MkdirAll(dir, 0755)
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir, cfg.Seed, cfg.ListenPort = dir, false, 0
	c, err := torrent.NewClient(cfg)
	if err != nil {
		panic(err)
	}
	defer c.Close()
	_, err = tea.NewProgram(model{client: c}, tea.WithAltScreen()).Run()
	if err != nil {
		panic(err)
	}
}
