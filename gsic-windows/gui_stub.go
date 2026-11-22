//go:build !windows

package main

// GUI stub for non-Windows builds (Linux smoke tests, CI).
// The real GUI (Walk window + tray icon) lives in gui.go and
// gui_walk.go and only compiles on Windows.
type GUI struct {
	cfg     *Config
	cards   *CardStore
	updater *Updater
	tsnet   *TsnetNode
}

func NewGUI(cfg *Config, cards *CardStore, updater *Updater) *GUI {
	return &GUI{cfg: cfg, cards: cards, updater: updater}
}

func (g *GUI) SetTsnet(n *TsnetNode) { g.tsnet = n }

func (g *GUI) SetWalkWindow(w *WalkMainWindow) {}

func (g *GUI) Run() {}

func (g *GUI) Show() {}

func (g *GUI) ShowMainWindow() {}

func (g *GUI) AppendLog(line string) {}

func (g *GUI) LogLines() []string { return nil }

func (g *GUI) Quit() {}

// WalkMainWindow stub for non-Windows builds.
type WalkMainWindow struct{}

func NewWalkMainWindow(cfg *Config, cards *CardStore, updater *Updater, gui *GUI) (*WalkMainWindow, error) {
	return nil, nil
}

func (w *WalkMainWindow) SetTsnet(n *TsnetNode) {}

func (w *WalkMainWindow) Show() {}

func (w *WalkMainWindow) Run() {}
