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
	"github.com/charmbracelet/x/ansi"
	"github.com/dustin/go-humanize"
)

type result struct {
	name, size, magnet, category, date string
	source, sourceURL, check, flag     string
	seeds, leechers                    int
}
type found []result
type failure struct{ error }
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
	dim  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	hot  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7DCFFF"))
	pick = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E0F2FE")).Background(lipgloss.Color("#1E3A5F"))
)

func search(q string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequest("GET", "https://knaben.org/search/"+url.PathEscape(q)+"/0/1/seeders", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return failure{err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return failure{fmt.Errorf("search returned %s", resp.Status)}
		}
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			return failure{err}
		}
		var rows []result
		doc.Find("tr[data-id]").EachWithBreak(func(_ int, tr *goquery.Selection) bool {
			a := tr.Find(`a[href^="magnet:"]`).First()
			magnet, ok := a.Attr("href")
			if !ok {
				return true
			}
			td := tr.Find("td")
			text := func(i int) string { return strings.TrimSpace(td.Eq(i).Text()) }
			number := func(i int) int { n, _ := strconv.Atoi(text(i)); return n }
			flag := strings.TrimSpace(td.Eq(1).Find(".badge").Last().Text())
			rows = append(rows, result{
				html.UnescapeString(strings.TrimSpace(a.AttrOr("title", text(1)))), text(2), magnet,
				strings.Join(strings.Fields(text(0)), " "), text(3), text(6),
				td.Eq(6).Find("a").First().AttrOr("href", ""),
				tr.Find("[data-bs-original-title]").First().AttrOr("data-bs-original-title", ""),
				flag, number(4), number(5),
			})
			return len(rows) < 10
		})
		return found(rows)
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
		if x.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if len(m.rows) == 0 {
			return m.searchKey(x)
		}
		return m.resultKey(x)
	case found:
		m.rows, m.cursor, m.status = x, 0, ""
	case failure:
		m.status = x.Error()
	case tick:
		if m.download == nil {
			break
		}
		done := m.download.BytesCompleted()
		m.rate, m.last = (done-m.last)*2, done
		switch {
		case m.download.Length() == 0:
			m.status = "finding metadata…"
		case m.download.BytesMissing() > 0:
			m.status = "downloading"
		default:
			m.status, m.rate = "complete", 0
			return m, nil
		}
		return m, pulse()
	}
	return m, nil
}

func (m model) searchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEnter:
		if strings.TrimSpace(m.query) != "" {
			m.status = "searching…"
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

func (m model) resultKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "esc", "/":
		m.rows, m.cursor, m.query, m.status = nil, 0, "", ""
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(m.rows)-1, m.cursor+1)
	case "o":
		_ = exec.Command("open", m.rows[m.cursor].sourceURL).Start()
	case "enter":
		t, err := m.client.AddMagnet(m.rows[m.cursor].magnet)
		if err != nil {
			m.status = err.Error()
			break
		}
		go func() { <-t.GotInfo(); t.DownloadAll() }()
		m.download, m.last, m.status = t, 0, "finding metadata…"
		return m, pulse()
	}
	return m, nil
}

func (m model) downloadView() string {
	total, done := m.download.Length(), m.download.BytesCompleted()
	if total == 0 {
		return hot.Render("○ " + m.status)
	}
	pct := min(1, float64(done)/float64(total))
	filled := int(pct * 32)
	bar := hot.Render(strings.Repeat("▓", filled)) + dim.Render(strings.Repeat("░", 32-filled))
	return fmt.Sprintf("%s\n%s  %5.1f%%\n%s  %s  %s/s  %d peers", hot.Render(m.download.Name()),
		bar, pct*100, humanize.Bytes(uint64(done)), dim.Render("of "+humanize.Bytes(uint64(total))),
		humanize.Bytes(uint64(max(0, m.rate))), m.download.Stats().ActivePeers)
}

func detailView(r result) string {
	status := r.check
	if r.flag != "" {
		status += "  •  ⚠ " + r.flag
	}
	meta := strings.Trim(strings.Join([]string{r.category, r.source, r.date}, "  •  "), "  •")
	return hot.Render("selected") + "\n" + ansi.Truncate(r.name, 76, "…") + "\n" + dim.Render(meta) + "\n" + dim.Render(status)
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
		for i, r := range m.rows[start:min(len(m.rows), start+visible)] {
			i += start
			line := fmt.Sprintf(" %2d  %-46.46s %8.8s %6d %5d ", i+1, r.name, r.size, r.seeds, r.leechers)
			if i == m.cursor {
				line = pick.Render("▍" + line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + dim.Render("↑↓ choose  •  enter download  •  o description  •  / search again") +
			"\n\n" + detailView(m.rows[m.cursor]))
	}
	if m.download != nil {
		b.WriteString("\n\n" + m.downloadView())
	} else if m.status != "" {
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
	if _, err = tea.NewProgram(model{client: c}, tea.WithAltScreen()).Run(); err != nil {
		panic(err)
	}
}
