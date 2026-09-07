package main

import (
	"bytes"
	"crypto/rand"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func localClient(t *testing.T, seed bool) (*torrent.Client, string) {
	t.Helper()
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir, cfg.Seed, cfg.ListenPort = t.TempDir(), seed, 0
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.NoDHT, cfg.DisableTrackers, cfg.NoDefaultPortForwarding = true, true, true
	cfg.DisableIPv6, cfg.DisableUTP, cfg.DisableWebtorrent = true, true, true
	c, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, cfg.DataDir
}

func TestLocalTransferAndSwitch(t *testing.T) {
	seed, seedDir := localClient(t, true)
	client, clientDir := localClient(t, false)
	data := make([]byte, 4<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sample.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: encoded}
	// Use a file path known to the seeder's storage.
	if err := os.WriteFile(filepath.Join(seedDir, "sample.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	st, err := seed.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	st.VerifyData()
	magnet := mi.Magnet(nil, &info).String()
	m := model{client: client, rows: []result{{magnet: magnet}}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd != nil {
		t.Error("Enter started another timer")
	}
	original := m.download
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd != nil || m.download != original || len(client.Torrents()) != 1 {
		t.Error("repeated Enter changed active transfer/timer")
	}
	m.download.AddClientPeer(seed)
	start := time.Now()
	deadline := start.Add(10 * time.Second)
	for !m.download.Complete().Bool() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		next, _ = m.Update(time.Now())
		m = next.(model)
	}
	if !m.download.Complete().Bool() {
		t.Fatal("local transfer did not complete within 10 seconds")
	}
	downloaded, err := os.ReadFile(filepath.Join(clientDir, "sample.bin"))
	if err != nil || !bytes.Equal(downloaded, data) {
		t.Fatalf("download differs: %v", err)
	}
	if !strings.Contains(strings.Join(m.downloadLines(78), "\n"), "100.0%") {
		t.Fatal("completion not displayed")
	}
	for _, size := range [][2]int{{60, 18}, {80, 24}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		if !strings.Contains(view, "Complete") {
			t.Fatalf("completion hidden at %v", size)
		}
		lines := strings.Split(view, "\n")
		if len(lines) > size[1]-1 {
			t.Fatal("download panel overflows vertically")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) >= size[0] {
				t.Fatal("download panel overflows horizontally")
			}
		}
	}
	t.Logf("verified %d bytes in %s", len(data), time.Since(start).Round(time.Millisecond))
	// Check elapsed-time speed math against real transfer counters.
	stats := m.download.Stats()
	now := time.Now()
	m.meter.last, m.meter.sampled = stats.BytesReadUsefulData.Int64()-4096, now.Add(-2*time.Second)
	next, _ = m.Update(now)
	m = next.(model)
	if m.meter.rate != 2048 {
		t.Fatalf("speed = %v, want 2048 B/s", m.meter.rate)
	}
	m.rows = []result{{magnet: "magnet:?xt=urn:btih:1111111111111111111111111111111111111111"}}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(client.Torrents()) != 1 || m.download == original {
		t.Fatal("old download still active")
	}
	select {
	case <-original.Closed():
	default:
		t.Fatal("old torrent not closed")
	}
	if _, err := os.Stat(filepath.Join(clientDir, "sample.bin")); err != nil {
		t.Fatal("switch removed files")
	}
	m.rows[0].magnet = "invalid magnet"
	active := m.download
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.download != active || len(client.Torrents()) != 1 {
		t.Fatal("invalid magnet discarded active transfer")
	}
	if !strings.Contains(m.View(), m.status) {
		t.Fatal("error is hidden during download")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchParsing(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "test query") {
			t.Errorf("wrong query URL: %s", r.URL)
		}
		body := `<table><tr data-id="1"><td>Video</td><td><a title="A &amp;amp; B" href="magnet:?xt=urn:btih:1111111111111111111111111111111111111111">title</a></td><td>4 MB</td><td>today</td><td>1,042</td><td>7</td><td><a href="https://example.com">source</a></td></tr></table>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Repeat(body, 2)))}, nil
	})
	next, _ := (model{}).Update(search("test query")())
	m := next.(model)
	if len(m.rows) != 1 || m.rows[0].name != "A &amp; B" || m.rows[0].seeds != 1042 || m.rows[0].leechers != 7 {
		t.Fatalf("bad search result: %+v", m)
	}
	if !strings.Contains(m.View(), "A &amp; B") {
		t.Fatal("search result missing from UI")
	}
}
