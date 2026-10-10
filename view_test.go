package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestViewsStayWithinTerminal(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {10, 4}, {59, 17}, {60, 18}, {80, 24}, {120, 40}} {
		m := model{width: size[0], height: size[1], query: strings.Repeat("界", 200)}
		for i := 0; i < 30; i++ {
			m.rows = append(m.rows, result{name: strings.Repeat("界", 80) + "\x1b[2J\n1080p.WEB-DL.H264", size: "123456789012 GB", seeds: 12345678, source: "\rbroken", flag: "\x1b]0;injected\a"})
		}
		m.cursor = 29
		for _, results := range []bool{true, false} {
			if !results {
				m.rows = nil
			}
			view := m.View()
			if strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\x1b]0;") {
				t.Fatal("remote terminal controls escaped")
			}
			lines := strings.Split(view, "\n")
			if len(lines) > max(1, size[1]-1) {
				t.Errorf("%v: %d rows", size, len(lines))
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > max(1, size[0]) {
					t.Errorf("%v: overlong line %q", size, line)
				}
			}
		}
	}
}
