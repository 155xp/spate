package main

import (
	"bytes"
	stdlog "log"
	"log/slog"
	"strings"
	"testing"
	"time"

	torrentlog "github.com/anacrolix/log"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMeterAndETA(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	var meter transferMeter
	meter.sample(now, 0)
	meter.sample(now.Add(2*time.Second), 4096)
	if meter.rate != 2048 || meter.average != 2048 {
		t.Fatalf("wrong rate: %+v", meter)
	}
	if got := eta(2048*120, meter.average, now); got != "~2m left · finishes ~12:02" {
		t.Fatal(got)
	}
	meter.sample(now.Add(3*time.Second), 12288)
	if meter.average <= 2048 || meter.average >= meter.rate {
		t.Fatalf("rate not smoothed: %+v", meter)
	}
	meter.sample(now.Add(13*time.Second), 12288)
	if meter.average != 0 || eta(100, meter.average, now) != "ETA estimating…" {
		t.Fatal("stale ETA after stall")
	}
	meter.sample(now.Add(14*time.Second), 14336)
	if meter.average != 2048 {
		t.Fatal("rate did not recover")
	}
	for _, tc := range []struct {
		bytes int64
		rate  float64
		want  string
	}{
		{0, 0, "ETA estimating…"}, {10, 100, "~1s left · finishes ~12:00"}, {100, 0, "ETA estimating…"},
		{3600, 1, "~1h0m left · finishes ~13:00"}, {1 << 60, 1, "ETA unavailable"},
	} {
		if got := eta(tc.bytes, tc.rate, now); got != tc.want {
			t.Errorf("ETA %v: %q", tc, got)
		}
	}
}

func TestReleaseHints(t *testing.T) {
	for _, tc := range []struct{ name, resolution, source, video, audio, language, display string }{
		{"Example.2026.1080p.AMZN.WEB-DL.DDP5.1.H264-GROUP", "1080P", "WEB-DL", "H.264/AVC", "DDP 5.1", "", ""},
		{"Example.2160p.UHD.BluRay.REMUX.HEVC.TrueHD.7.1.Atmos.DV.MULTI", "2160P", "REMUX", "H.265/HEVC", "Atmos 7.1", "MULTI", "Dolby Vision"},
		{"Example.720p.HDCAM.ENG", "720P", "CAM/TS", "", "", "ENG", ""},
		{"Example.1080p.WEBRip.AV1.AAC.5.1", "1080P", "WEBRip", "AV1", "AAC 5.1", "", ""},
		{"Linux desktop ISO", "", "", "", "", "", ""},
	} {
		r := describeRelease(tc.name)
		if r.resolution != tc.resolution || r.source != tc.source || r.video != tc.video || r.audio != tc.audio || r.language != tc.language || r.display != tc.display {
			t.Errorf("%q: %+v", tc.name, r)
		}
	}
	if !strings.Contains(describeRelease("Film.HDCAM.HDR").note, "poor") {
		t.Fatal("camera warning hidden")
	}
}

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

func TestLogsLeaveRendererAlone(t *testing.T) {
	oldTorrent, oldSlog, oldWriter := torrentlog.Default, slog.Default(), stdlog.Writer()
	t.Cleanup(func() { torrentlog.Default = oldTorrent; slog.SetDefault(oldSlog); stdlog.SetOutput(oldWriter) })
	var buffer bytes.Buffer
	configureLogs(&buffer)
	torrentlog.Default.Levelf(torrentlog.Warning, "tracker-test")
	slog.Warn("slog-test")
	stdlog.Print("standard-test")
	for _, marker := range []string{"tracker-test", "slog-test", "standard-test"} {
		if !strings.Contains(buffer.String(), marker) {
			t.Errorf("missing log %q", marker)
		}
	}
}

func TestSearchErrorsAndRepeatEnter(t *testing.T) {
	m := model{query: "test"}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("search not started")
	}
	m = next.(model)
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("duplicate search started")
	}
	next, _ = m.Update([]result{})
	if !strings.Contains(next.(model).View(), "No results") {
		t.Fatal("missing empty result message")
	}
}
