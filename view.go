package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/dustin/go-humanize"
)

var (
	dim  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	hot  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7DCFFF"))
	pick = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E0F2FE")).Background(lipgloss.Color("#1E3A5F"))
)

type transferMeter struct {
	last              int64
	sampled, progress time.Time
	rate, average     float64
}

func (m *transferMeter) sample(now time.Time, received int64) {
	if m.sampled.IsZero() {
		m.last, m.sampled, m.progress = received, now, now
		return
	}
	seconds := now.Sub(m.sampled).Seconds()
	if seconds <= 0 {
		return
	}
	m.rate = float64(max(0, received-m.last)) / seconds
	if received > m.last {
		m.progress = now
	}
	if m.average == 0 {
		m.average = m.rate
	} else {
		m.average += (1 - math.Exp(-seconds/8)) * (m.rate - m.average)
	}
	if now.Sub(m.progress) >= 10*time.Second {
		m.average = 0
	}
	m.last, m.sampled = received, now
}

func eta(remaining int64, speed float64, now time.Time) string {
	if speed <= 0 {
		return "ETA estimating…"
	}
	seconds := math.Ceil(float64(max(0, remaining)) / speed)
	if seconds > 365*24*3600 {
		return "ETA unavailable"
	}
	d := time.Duration(seconds) * time.Second
	duration := d.Round(time.Second).String()
	if d >= time.Minute {
		duration = d.Round(time.Minute).String()
		duration = strings.TrimSuffix(duration, "0s")
	}
	return fmt.Sprintf("~%s left · finishes ~%s", duration, now.Add(d).Format("15:04"))
}

// Remote text cannot inject terminal controls or extra layout lines.
func plain(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(s))), " ")
}

func cell(s string, width int) string {
	s = ansi.Truncate(plain(s), max(0, width), "…")
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}

func (m model) downloadLines(width int) []string {
	t := m.download
	stats := t.Stats()
	peers := fmt.Sprintf("%d peers · %d connected seeders", stats.ActivePeers, stats.ConnectedSeeders)
	if t.Info() == nil {
		return []string{hot.Render("DOWNLOAD  " + plain(t.Name())), "Finding metadata…", dim.Render(peers)}
	}
	total, done := t.Length(), t.BytesCompleted()
	fraction := float64(done) / float64(max(1, total))
	barWidth := max(8, min(48, width-10))
	filled := min(barWidth, max(0, int(fraction*float64(barWidth))))
	bar := hot.Render(strings.Repeat("━", filled)) + dim.Render(strings.Repeat("─", barWidth-filled))
	timing := eta(t.BytesMissing(), m.meter.average, m.meter.sampled)
	switch {
	case t.Complete().Bool():
		timing = "Complete · saved to ~/Downloads/spate"
	case t.BytesMissing() == 0:
		timing = "Checking downloaded pieces…"
	case !m.meter.progress.IsZero() && m.meter.sampled.Sub(m.meter.progress) >= 10*time.Second:
		timing = "Waiting for data · ETA unavailable"
	}
	return []string{
		hot.Render("DOWNLOAD  " + plain(t.Name())),
		fmt.Sprintf("%s %5.1f%%", bar, min(100, fraction*100)),
		fmt.Sprintf("%s / %s · %s/s · %d files", humanize.Bytes(uint64(done)), humanize.Bytes(uint64(total)), humanize.Bytes(uint64(max(0, m.meter.rate))), len(t.Files())),
		timing,
		dim.Render(peers),
	}
}

func detailLines(r result, compact bool) []string {
	tags := describeRelease(r.name)
	lines := []string{hot.Render(plain(r.name)), "Title tags: " + tags.summary(), tags.note}
	if !compact {
		availability := fmt.Sprintf("%d listed seeders · %d downloading", r.seeds, r.leechers)
		if r.seeds == 0 {
			availability += " · may be unavailable"
		}
		lines = append(lines, availability, dim.Render(joinDetails(r.flag, r.category, r.source, r.date, r.check)))
	}
	return lines
}

func (m model) View() string {
	width, height := m.width, m.height
	if width > 0 && width < 3 {
		return ""
	}
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}
	// Leave the last column and row free: writing into them can trigger terminal scrolling.
	w, h := max(1, width-2), max(1, height-1)
	lines := []string{hot.Render("spate") + dim.Render("   search · compare · download"), ""}
	if len(m.rows) > 0 {
		lines[0] = hot.Render("spate") + dim.Render(fmt.Sprintf("   %d / %d   ", m.cursor+1, len(m.rows))) + ansi.Truncate(plain(m.query), max(1, w-23), "…")
	}
	if width < 60 || height < 18 {
		lines = []string{hot.Render("spate"), "Resize to at least 60 × 18", "Ctrl+C to quit"}
	} else {
		help := "enter search · ctrl+c quit"
		var download []string
		if m.download != nil {
			download = append([]string{""}, m.downloadLines(w)...)
		}
		footer := []string{"", dim.Render(help)}
		if m.status != "" {
			footer = append(footer, hot.Render(plain(m.status)))
		}
		if len(m.rows) == 0 {
			// Keep the end of a long query visible without wrapping.
			query := plain(m.query)
			if ansi.StringWidth(query) > w-5 {
				query = ansi.TruncateLeft(query, ansi.StringWidth(query)-(w-6), "…")
			}
			lines = append(lines, "❯ "+query+"█")
		} else {
			footer[1] = dim.Render("↑↓ choose · enter download · o source · / search · q quit")
			detail := detailLines(m.rows[m.cursor], height < 24)
			visible := max(1, h-len(lines)-len(detail)-len(download)-len(footer)-2)
			start := max(0, min(m.cursor-visible+1, len(m.rows)-visible))
			nameWidth := w - 39
			lines = append(lines, dim.Render(cell("RELEASE", nameWidth)+cell("SIZE", 10)+cell("SEEDS", 7)+"QUALITY / SOURCE"))
			for i := start; i < min(len(m.rows), start+visible); i++ {
				r := m.rows[i]
				marker := "  "
				if i == m.cursor {
					marker = "› "
				}
				tags := describeRelease(r.name)
				line := marker + cell(r.name, nameWidth-2) + cell(r.size, 10) + cell(humanize.Comma(int64(r.seeds)), 7) + strings.TrimSpace(tags.resolution+" "+tags.source)
				if i == m.cursor {
					line = pick.Render(line)
				}
				lines = append(lines, line)
			}
			lines = append(lines, "")
			lines = append(lines, detail...)
		}
		lines = append(lines, download...)
		lines = append(lines, footer...)
	}
	lines = lines[:min(len(lines), h)]
	for i, line := range lines {
		lines[i] = " " + ansi.Truncate(line, w, "…")
	}
	return strings.Join(lines, "\n")
}
