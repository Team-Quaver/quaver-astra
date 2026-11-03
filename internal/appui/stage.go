package appui

import (
	"math"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// stageLyricData is the normalized lyric source used by the 辞曲 stage.
// Word-timed QRC and line-level LRC both feed the same line model, so the
// stage does not need two near-identical renderers.
type stageLyricData struct {
	lines  []player.QrcLine
	state  string
	source string
}

// lyricStageKey chooses the now-playing layout: classic or folia.
const lyricStageKey = "Lyrics.Stage"

func (a *App) lyricStage() string {
	if a.Conf == nil {
		return "classic"
	}
	if a.Conf.String(lyricStageKey, "classic") == "folia" {
		return "folia"
	}
	return "classic"
}

func (a *App) setLyricStage(stage string) {
	if stage != "folia" {
		stage = "classic"
	}
	if a.Conf != nil {
		a.Conf.Set(lyricStageKey, stage)
	}
	a.invalidate()
}

func (a *App) stageLyrics() stageLyricData {
	if qrc := a.PL.QrcLines(); len(qrc) > 0 {
		return stageLyricData{lines: qrc, state: "ok", source: "QQ 音乐 QRC"}
	}
	lines, state := a.PL.LyricLines()
	out := make([]player.QrcLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, player.QrcLine{
			Time: line.Time, Text: line.Text, Trans: line.Trans, Roma: line.Roma,
			Dur: nextLyricDuration(lines, line.Time),
		})
	}
	return stageLyricData{lines: out, state: state, source: "LRC"}
}

func nextLyricDuration(lines []player.LyricLine, at float64) float64 {
	for _, line := range lines {
		if line.Time > at+0.001 {
			return line.Time - at
		}
	}
	return 0
}

func (a *App) stageLyricSource() string {
	if len(a.PL.QrcLines()) > 0 {
		return "QQ 音乐 QRC"
	}
	return ""
}

func stageIndexAt(lines []player.QrcLine, t float64) int {
	at := -1
	for i := range lines {
		if lines[i].Time <= t+0.001 {
			at = i
		} else {
			break
		}
	}
	return at
}

func stageAux(line player.QrcLine, mode string) string {
	switch mode {
	case "translation":
		if line.Trans != "" {
			return line.Trans
		}
		return line.Roma
	case "romanization":
		if line.Roma != "" {
			return line.Roma
		}
		return line.Trans
	}
	return ""
}

// foliaStage is the typography-led immersive layout. It follows Folia's
// core idea (the current line is the composition; metadata is secondary)
// without copying its controls or Apple-like floating pill.
func (a *App) foliaStage(c *ui.Context, t *ui.Theme, cur player.Song, hasCur bool) {
	data := a.stageLyrics()
	pos := a.lyricPos(c)
	curLine := stageIndexAt(data.lines, pos)
	if data.state == "ok" && curLine >= 0 && curLine != a.lastLyricLine {
		a.lastLyricLine = curLine
		if !a.lyricFollowPaused(c.Now()) {
			a.followLyricLine(curLine)
		}
	}

	a.foliaAtmosphere(c, t, cur)
	// The pane tabs live at the page's top edge (npTopControls); the stage
	// starts below them, so the two do not draw over each other.
	content := ui.Column(c).Absolute().Fill().Padding(62, 54, 26, 54).Gap(12)
	content.Children(func() {
		switch {
		case data.state == "loading":
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		case data.state == "none":
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "这首歌暂时没有歌词").FontSize(fz(16)).
					TextColor(ui.RGBA(255, 255, 255, 0.52))
			})
		case len(data.lines) == 0:
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, hasCurTitle(cur, hasCur)).FontSize(fz(24)).FontWeight(800).
					TextColor(ui.Hex("#ffffff"))
				ui.Text(c, "让音乐先说话").FontSize(fz(12)).
					TextColor(ui.RGBA(255, 255, 255, 0.45))
			})
		default:
			a.npList.Label = func(i int) string { return data.lines[i].Text }
			a.lyricList(c, len(data.lines), curLine, func(i int) {
				a.foliaLyricLine(c, t, data.lines[i], i, i == curLine)
			})
		}

		if hasCur {
			a.foliaTrackCard(c, t, cur, data.source)
		}
	})
}

func hasCurTitle(cur player.Song, ok bool) string {
	if ok && cur.DisplayName() != "" {
		return cur.DisplayName()
	}
	return "Quaver Astra"
}

func (a *App) stageSourceBadge(c *ui.Context, source string, t *ui.Theme) {
	ui.Row(c).Padding(3, 8).Radius(999).Border(1, t.Accent.Alpha(0.5)).
		Background(t.Accent.Alpha(0.11)).Children(func() {
		ui.Text(c, source).FontSize(fz(9.5)).FontWeight(700).TextColor(t.Accent)
	})
}

// foliaAtmosphere draws slow orbital geometry and a ghosted cover. The
// animation stops when playback stops, so a paused window is completely
// still apart from user-driven interaction.
func (a *App) foliaAtmosphere(c *ui.Context, t *ui.Theme, cur player.Song) {
	if cur.Mid != "" {
		if art := a.Covers.Get(coverURL(cur.AlbumPmid, cur.AlbumMid, 500), a.invalidate); art != nil {
			ui.Image(c, art).Absolute().Top(-110).Right(-130).Size(620, 620).Fit(ui.Cover).
				Radius(310).Clip().Opacity(0.10)
		}
	}
	ui.Box(c).Absolute().Fill().
		Gradient(ui.RGBA(8, 10, 18, 0.58), ui.RGBA(8, 10, 18, 0.04), 90)

	deco := ui.Box(c).Absolute().Fill()
	deco.Draw(func(p *ui.Painter, r ui.Rect) {
		moving := a.PL.Playing()
		if moving {
			p.AnimationFrame()
		}
		now := p.Now().UnixNano() / int64(time.Millisecond)
		drift := float64(0)
		if moving {
			drift = float64(now%240000) / 240000
		}
		cx := r.X + r.W*0.72 + float32(math.Sin(drift*math.Pi*2)*18)
		cy := r.Y + r.H*0.46 + float32(math.Cos(drift*math.Pi*2)*12)
		line := t.Accent.Alpha(0.13)
		var orbit ui.Path
		for _, rad := range []float32{115, 205, 320} {
			orbit.Circle(cx, cy, rad)
		}
		p.StrokePath(&orbit, 1, line)

		// A four-point star echoes Folia's type-as-image rhythm without
		// reproducing any of its themes.
		var star ui.Path
		angle := float32(0)
		if moving {
			angle = float32(drift * math.Pi * 0.5)
		}
		points := 8
		for i := 0; i <= points; i++ {
			a := angle + float32(i)*float32(math.Pi*2/float64(points))
			rad := float32(78)
			if i%2 == 1 {
				rad = 24
			}
			x := cx + float32(math.Cos(float64(a)))*rad
			y := cy + float32(math.Sin(float64(a)))*rad
			if i == 0 {
				star.MoveTo(x, y)
			} else {
				star.LineTo(x, y)
			}
		}
		p.StrokePath(&star, 1.4, t.Accent.Alpha(0.20))

		// Sparse ticks keep the empty space alive on wide windows.
		tick := ui.RGBA(255, 255, 255, 0.10)
		for i := 0; i < 5; i++ {
			x := r.X + r.W*(0.12+float32(i)*0.17)
			y := r.Y + r.H*(0.18+float32((i*3)%4)*0.17)
			p.Line(x, y, x+7, y, 1, tick)
			p.Line(x+3.5, y-3.5, x+3.5, y+3.5, 1, tick)
		}
	})
}

func (a *App) foliaLyricLine(c *ui.Context, t *ui.Theme, line player.QrcLine, index int, current bool) {
	pos := a.lyricPos(c)
	if current && a.PL.Playing() && pos < line.End() {
		c.AnimationFrame()
	}
	align := ui.Start
	row := ui.Column(c).FillWidth().Padding(5, 12).Gap(5).Radius(10).
		Cursor(ui.CursorPointer).Transition(hoverFade)
	if current {
		a.followLyricRow(c, row, index)
	}
	row.Children(func() {
		if !current {
			ui.Text(c, line.Text).Font(a.lyricFontFamily()).FontSize(fz(17)).
				FontWeight(500).TextColor(ui.RGBA(255, 255, 255, 0.30)).
				MaxLines(2).TextAlign(align)
			return
		}
		a.foliaCurrentWords(c, t, line, pos, align)
		if aux := stageAux(line, a.PL.LyricAuxMode()); aux != "" {
			ui.Text(c, aux).Font(a.lyricFontFamily()).FontSize(fz(16)).
				TextColor(ui.RGBA(255, 255, 255, 0.68)).MaxLines(3).TextAlign(align)
		}
	})
	if row.Hovered() && !current {
		row.Background(ui.RGBA(255, 255, 255, 0.05))
	}
	if row.Clicked() {
		if d := a.PL.Duration(); d > 0 {
			a.PL.SeekTo(line.Time / d)
		}
	}
}

func (a *App) foliaCurrentWords(c *ui.Context, t *ui.Theme, line player.QrcLine, pos float64, align ui.Align) {
	if len(line.Words) == 0 {
		ui.Text(c, line.Text).Font(a.lyricFontFamily()).FontSize(fz(38)).
			FontWeight(800).TextColor(ui.Hex("#ffffff")).MaxLines(3).TextAlign(align).
			Shadow(0, 8, 30, 0, ui.RGBA(0, 0, 0, 0.35))
		return
	}
	fills := line.WordFills(pos, a.karaBuf[:0])
	a.karaBuf = fills
	w, _ := c.Size()
	size := float32(42)
	if w > 1200 {
		size = 52
	}
	if w > 1800 {
		size = 64
	}
	// A single shaped paragraph keeps the line's own word spacing, kerning
	// and wrapping. Splitting it into Row children drops the QRC spacer
	// words and turns Latin spaces into zero-width gaps.
	a.karaokeRubyLine(c, line, true, size, npFoliaLyricWidth(c), align, func(fill float32) ui.Color {
		if fill >= 1 {
			return ui.Hex("#ffffff")
		}
		if fill > 0 {
			return t.Accent.Mix(ui.Hex("#ffffff"), 0.35)
		}
		return ui.RGBA(255, 255, 255, 0.44)
	}, true)
}

func (a *App) foliaTrackCard(c *ui.Context, t *ui.Theme, cur player.Song, source string) {
	card := ui.Row(c).FillWidth().Padding(10, 12).Gap(12).Radius(16).
		Background(ui.RGBA(10, 13, 22, 0.46)).Border(1, ui.RGBA(255, 255, 255, 0.10)).
		Shadow(0, 12, 34, 0, ui.RGBA(0, 0, 0, 0.26))
	card.Children(func() {
		if art := a.Covers.Get(coverURL(cur.AlbumPmid, cur.AlbumMid, 300), a.invalidate); art != nil {
			ui.Image(c, art).Size(54, 54).Fit(ui.Cover).Radius(10)
		} else {
			ui.Box(c).Size(54, 54).Radius(10).Background(ui.RGBA(255, 255, 255, 0.08)).
				Center().Children(func() {
				ui.Icon(c, Icons["note"]).FontSize(fz(22)).TextColor(ui.RGBA(255, 255, 255, 0.5))
			})
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, cur.DisplayName()).FontSize(fz(14)).FontWeight(800).
				TextColor(ui.Hex("#ffffff")).SingleLine().Ellipsis("…")
			ui.Text(c, cur.Artists).FontSize(fz(11)).TextColor(ui.RGBA(255, 255, 255, 0.56)).
				SingleLine().Ellipsis("…")
		})
		ui.Column(c).AlignItems(ui.End).Gap(4).Children(func() {
			if source != "" {
				ui.Text(c, source).FontSize(fz(9.5)).FontWeight(700).
					TextColor(t.Accent).SingleLine()
			}
			ui.Textf(c, "%s / %s", fmtTime(a.PL.Position()), fmtTime(a.PL.Duration())).
				FontSize(fz(10.5)).TextColor(ui.RGBA(255, 255, 255, 0.5)).Font("monospace")
		})
	})
}
