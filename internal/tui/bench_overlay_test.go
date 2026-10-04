package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/usagestats"
)

// Overlay frame-cost benchmarks. Every scenario runs on the same heavy
// transcript at 220x60 as BenchmarkViewHeavy so numbers are comparable with
// the existing frame benchmarks.
//
//	go test ./internal/tui -run '^$' -bench 'BenchmarkOverlay/' -benchmem
//
// Sub-benchmarks per scenario:
//
//	stationary  View() with nothing changed since the previous frame
//	key         one Update(key) + View(); keys cycle through the scenario's list
//	type        one Update(typed char / backspace) + View() (filterable overlays)
//	wheel       one Update(mouse wheel) + View(), alternating down/up
//
// The "closed" scenario is the no-overlay baseline for each of them.

type overlayScenario struct {
	name string
	// open puts the model into the overlay state, mimicking the production
	// open path. It runs once, outside the timed region.
	open func(b *testing.B, m *Model)
	// view returns the overlay's own rendered string (nil when not applicable).
	view func(m *Model) string
	// keys cycle one per iteration for the "key" sub-benchmark.
	keys []tea.Msg
	// typed cycle one per iteration for the "type" sub-benchmark.
	typed []tea.Msg
	// wheelX/wheelY is the pointer position for wheel events.
	wheelX, wheelY int
}

var (
	benchKeyDown = tea.KeyPressMsg{Code: tea.KeyDown}
	benchKeyUp   = tea.KeyPressMsg{Code: tea.KeyUp}
	benchKeyPgDn = tea.KeyPressMsg{Code: tea.KeyPgDown}
	benchKeyLeft = tea.KeyPressMsg{Code: tea.KeyLeft}
	benchKeyRght = tea.KeyPressMsg{Code: tea.KeyRight}
	benchBackspc = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

func benchRune(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func newOverlayBenchModel(b *testing.B) *Model {
	b.Helper()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModelHeavy(m)
	m.syncViewport()
	return m
}

func benchFilePaths(n int) []string {
	paths := make([]string, 0, n+2)
	for i := 0; len(paths) < n; i++ {
		dir := fmt.Sprintf("internal/pkg%03d", i%120)
		if i < 120 {
			paths = append(paths, dir+"/")
		}
		paths = append(paths, fmt.Sprintf("%s/component_%04d_handler.go", dir, i), fmt.Sprintf("%s/component_%04d_handler_test.go", dir, i))
	}
	return paths[:n]
}

func benchModelEntries(n int) []ModelEntry {
	providers := []string{"anthropic", "openai", "google", "deepseek", "mistral", "openrouter", "local"}
	sizes := []string{"mini", "pro", "flash", "large", "turbo"}
	entries := make([]ModelEntry, n)
	for i := range entries {
		ref := fmt.Sprintf("%s/model-%03d-%s", providers[i%len(providers)], i, sizes[i%len(sizes)])
		entries[i] = ModelEntry{Ref: ref, Display: ref, SupportedEfforts: []string{"low", "medium", "high"}, Current: i == 0}
	}
	return entries
}

func benchMCPServers() []MCPServerStatus {
	var servers []MCPServerStatus
	for i := 0; i < 8; i++ {
		s := MCPServerStatus{Name: fmt.Sprintf("server-%d", i), State: "connected", Transport: "stdio"}
		for t := 0; t < 15+i*3; t++ {
			outcome := "registered"
			if t%7 == 3 {
				outcome = "filtered"
			}
			s.Tools = append(s.Tools, MCPToolStatus{Name: fmt.Sprintf("tool_%d_%02d", i, t), Outcome: outcome})
		}
		if i == 5 {
			s.State, s.Tools, s.Error = "failed", nil, "connection refused: dial tcp 127.0.0.1:9000"
		}
		servers = append(servers, s)
	}
	return servers
}

func benchLSPServers() []LSPServerStatus {
	now := time.Now()
	var servers []LSPServerStatus
	for i, name := range []string{"gopls", "typescript", "rust-analyzer", "pyright", "clangd", "lua-ls"} {
		st := "ready"
		if i == 4 {
			st = "failed"
		}
		servers = append(servers, LSPServerStatus{Name: name, Root: "/home/user/project", Status: st, StartedAt: now, LastUsed: now})
	}
	return servers
}

func benchContextReport() string {
	var sb strings.Builder
	sb.WriteString("# Context report\n\n| Source | Tokens | Share |\n|---|---:|---:|\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&sb, "| source-%02d | %d | %d%% |\n", i, 1000+i*137, i%17)
	}
	for s := 0; s < 6; s++ {
		fmt.Fprintf(&sb, "\n## Section %d\n\nSome **prose** describing `item_%d` with a [link](https://example.com) and more words to wrap across the overlay width for realism.\n\n", s, s)
		for i := 0; i < 8; i++ {
			fmt.Fprintf(&sb, "- bullet %d.%d with `code` and trailing text\n", s, i)
		}
	}
	return sb.String()
}

func benchScenarios() []overlayScenario {
	updown := []tea.Msg{benchKeyDown, benchKeyDown, benchKeyDown, benchKeyUp, benchKeyUp, benchKeyUp}
	return []overlayScenario{
		{name: "closed", open: func(*testing.B, *Model) {}, keys: []tea.Msg{benchRune('a'), benchBackspc}, wheelX: 110, wheelY: 30},
		{
			name: "help",
			open: func(_ *testing.B, m *Model) { m.helpVisible = true },
			view: func(m *Model) string { return renderHelp(m.styles, max(20, m.contentWidth()-4)) },
			// help has no key handling of its own; keys fall through to the composer
			keys:   []tea.Msg{benchRune('a'), benchBackspc},
			wheelX: 110, wheelY: 30,
		},
		{
			name: "mcp",
			open: func(_ *testing.B, m *Model) {
				m.mcpServers, m.mcpEnabled = benchMCPServers(), true
				m.executeShowMCPAction()
			},
			view: func(m *Model) string { return m.mcpOverlay.View() },
			keys: []tea.Msg{benchKeyDown, benchKeyDown, benchKeyPgDn, benchKeyUp, benchKeyUp, benchKeyUp}, wheelX: 110, wheelY: 30,
		},
		{
			name: "lsp",
			open: func(_ *testing.B, m *Model) {
				m.lspServers, m.lspEnabled = benchLSPServers(), true
				m.executeShowLSPAction()
			},
			view: func(m *Model) string { return m.lspOverlay.View() },
			keys: updown, wheelX: 110, wheelY: 30,
		},
		{
			name: "filelist",
			open: func(b *testing.B, m *Model) {
				m.executeListFilesAction(b.TempDir())
				m.fileList.entries = benchFilePaths(3000)
			},
			view: func(m *Model) string { return m.fileList.View() },
			keys: updown, wheelX: 110, wheelY: 30,
		},
		{
			name: "context",
			open: func(_ *testing.B, m *Model) {
				m.contextOverlay = openContextOverlay("Context", benchContextReport(), m.width, m.height, m.styles, m.content.glamourStyleSheet)
			},
			view: func(m *Model) string { return m.renderContextOverlay() },
			keys: updown, wheelX: 110, wheelY: 30,
		},
		{
			name: "cachestats",
			open: func(b *testing.B, m *Model) {
				b.Setenv("XDG_STATE_HOME", b.TempDir())
				now := time.Unix(1000, 0)
				rec := usagestats.New(func() time.Time { return now })
				for p := 0; p < 4; p++ {
					for mo := 0; mo < 6; mo++ {
						rec.Record(usagestats.Observation{
							ProviderAlias: fmt.Sprintf("prov%d", p), ProviderType: "openai", BackendModelID: fmt.Sprintf("model-%d", mo),
							PromptTokens: 1000, CompletionTokens: 200, CacheReadTokens: 500, At: now,
						})
					}
				}
				m.recorder = rec
				m.executeOpenCacheStatsAction()
			},
			view: func(m *Model) string { return m.renderContextOverlay() },
			keys: updown, wheelX: 110, wheelY: 30,
		},
		{
			name: "delegatecancel",
			open: func(_ *testing.B, m *Model) {
				for i := 0; i < 3; i++ {
					id := fmt.Sprintf("agent_live_%d", i)
					updateModelDirect(m, runtimeEventMsg{Event: output.NewDelegationStartedEvent(agentOcc(id), "live task "+id, "", "")})
				}
				m.syncViewport()
				m.openDelegateCancelModal()
			},
			view: func(m *Model) string { return m.renderDelegateCancelModal() },
			keys: []tea.Msg{benchKeyRght, benchKeyLeft}, wheelX: 110, wheelY: 30,
		},
		{
			name: "slash",
			open: func(_ *testing.B, m *Model) {
				m.input.SetValue("/c")
				m.input.CursorEnd()
				m.slashOverlay = m.slashOverlay.Open(m.buildSlashOverlayItems())
				m.slashOverlay.width, m.slashOverlay.height = m.width, m.height
				m.syncSlashOverlayWithComposer()
			},
			view:  func(m *Model) string { return m.slashOverlay.View() },
			keys:  updown,
			typed: []tea.Msg{benchRune('o'), benchBackspc}, wheelX: 110, wheelY: 30,
		},
		{
			name: "filepicker",
			open: func(b *testing.B, m *Model) {
				m.input.SetValue("@s")
				m.input.CursorEnd()
				f := m.filePicker.Open(b.TempDir())
				f.allEntries = benchFilePaths(3000)
				f.candidates = append([]string(nil), f.allEntries...)
				f.matchIndexes = make([][]int, len(f.candidates))
				f.width, f.height = m.width, m.height
				m.filePicker = f
				m.syncFilePickerWithComposer()
			},
			view:  func(m *Model) string { return m.filePicker.View() },
			keys:  updown,
			typed: []tea.Msg{benchRune('e'), benchBackspc}, wheelX: 110, wheelY: 30,
		},
		{
			name: "modelpicker",
			open: func(_ *testing.B, m *Model) {
				m.modelEntries = benchModelEntries(400)
				m.executeOpenModelPickerAction()
				m.modelPicker, _ = m.modelPicker.Update(benchRune('o')) // standing query so both typed paths filter
			},
			view:  func(m *Model) string { return m.modelPicker.View() },
			keys:  updown,
			typed: []tea.Msg{benchRune('e'), benchBackspc}, wheelX: 110, wheelY: 30,
		},
	}
}

// BenchmarkOverlay measures full-frame cost (Update + View) per overlay class.
func setupOverlayBench(b *testing.B, sc overlayScenario) *Model {
	m := newOverlayBenchModel(b)
	sc.open(b, m)
	for range 3 { // warm every cache
		benchViewSink = m.View().Content
	}
	return m
}

func benchOverlayStationary(b *testing.B, sc overlayScenario) {
	m := setupOverlayBench(b, sc)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchViewSink = m.View().Content
	}
}

func benchOverlayStationaryByName(b *testing.B, name string) {
	for _, sc := range benchScenarios() {
		if sc.name == name {
			benchOverlayStationary(b, sc)
			return
		}
	}
	b.Fatalf("%s scenario missing", name)
}

func BenchmarkOverlayMCPStationary(b *testing.B)     { benchOverlayStationaryByName(b, "mcp") }
func BenchmarkOverlayContextStationary(b *testing.B) { benchOverlayStationaryByName(b, "context") }
func BenchmarkOverlayHelpStationary(b *testing.B)    { benchOverlayStationaryByName(b, "help") }
func BenchmarkOverlaySlashStationary(b *testing.B)   { benchOverlayStationaryByName(b, "slash") }
func BenchmarkOverlayModelPickerStationary(b *testing.B) {
	benchOverlayStationaryByName(b, "modelpicker")
}

func BenchmarkOverlayFilePickerType(b *testing.B) {
	for _, sc := range benchScenarios() {
		if sc.name != "filepicker" {
			continue
		}
		m := setupOverlayBench(b, sc)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m = updateModelDirect(m, sc.typed[i%len(sc.typed)])
			benchViewSink = m.View().Content
		}
		return
	}
	b.Fatal("filepicker scenario missing")
}

func BenchmarkOverlay(b *testing.B) {
	for _, sc := range benchScenarios() {
		b.Run(sc.name, func(b *testing.B) {
			setup := func(b *testing.B) *Model { return setupOverlayBench(b, sc) }
			b.Run("stationary", func(b *testing.B) { benchOverlayStationary(b, sc) })
			run := func(name string, msgs []tea.Msg) {
				if len(msgs) == 0 {
					return
				}
				b.Run(name, func(b *testing.B) {
					m := setup(b)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						m = updateModelDirect(m, msgs[i%len(msgs)])
						benchViewSink = m.View().Content
					}
				})
			}
			run("key", sc.keys)
			run("type", sc.typed)
			b.Run("wheel", func(b *testing.B) {
				m := setup(b)
				down := mouseWheelMsg{direction: "down", x: sc.wheelX, y: sc.wheelY}
				up := mouseWheelMsg{direction: "up", x: sc.wheelX, y: sc.wheelY}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if i%2 == 0 {
						m = updateModelDirect(m, down)
					} else {
						m = updateModelDirect(m, up)
					}
					benchViewSink = m.View().Content
				}
			})
		})
	}
}

// BenchmarkOverlayParts splits a stationary overlay frame into its stages:
// base (renderBaseView, cache hits), overlay (the overlay's own View string)
// and stage (renderOverlayView over a fixed base: overlay render + composition).
func BenchmarkOverlayParts(b *testing.B) {
	for _, sc := range benchScenarios() {
		if sc.view == nil {
			continue
		}
		b.Run(sc.name, func(b *testing.B) {
			m := newOverlayBenchModel(b)
			sc.open(b, m)
			cw := m.contentWidth()
			sidebar := m.sidebar.Visible(m.width)
			base := m.renderBaseView(cw, sidebar)
			b.Run("base", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					benchViewSink = m.renderBaseView(cw, sidebar)
				}
			})
			b.Run("overlay", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					benchViewSink = sc.view(m)
				}
			})
			b.Run("stage", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					benchViewSink = m.renderOverlayView(base, cw)
				}
			})
		})
	}
}
