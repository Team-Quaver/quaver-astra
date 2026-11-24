package ui

import (
	"math"
	"slices"
	"time"
)

// wheelAnimation is how the notches of a mouse wheel scroll: along a short
// ease out, as browsers scroll them, rather than the whole notch at once.
var wheelAnimation = Animation{Duration: 150 * time.Millisecond, Ease: EaseOut}

const (
	// wheelMomentumGap is how still the wheel is before its notches
	// coast, and wheelMomentumHold how long after the last notch they
	// may still start to: a frame that comes much later, as when the
	// window was hidden, does not set the content moving again.
	wheelMomentumGap  = 50 * time.Millisecond
	wheelMomentumHold = 200 * time.Millisecond
	// wheelMomentumWindow is the longest pause between notches that
	// counts toward their speed: a slow wheel, one notch at a time, does
	// not coast.
	wheelMomentumWindow = 100 * time.Millisecond
	// wheelMomentumTau is the time constant of the coast: its distance
	// is the speed times this, and its speed falls by e every tau.
	wheelMomentumTau = 150 * time.Millisecond
	// wheelMomentumMax caps the distance the notches coast, and
	// wheelMomentumMin the speed below which they do not; what falls
	// below wheelMomentumStop is at rest.
	wheelMomentumMax  = 600.0
	wheelMomentumMin  = 200.0
	wheelMomentumStop = 20.0

	// A touchpad's or a touchscreen's precise scrolling follows the
	// fingers at once, and coasts after they leave: trackpadMomentumGap is
	// how still they are before it does, and trackpadMomentumHold how long
	// after their last move it may still start. Its coast is gentler and
	// goes further than a wheel's.
	trackpadMomentumGap    = 30 * time.Millisecond
	trackpadMomentumHold   = 250 * time.Millisecond
	trackpadMomentumWindow = 100 * time.Millisecond
	trackpadMomentumTau    = 220 * time.Millisecond
	trackpadMomentumMax    = 1000.0
	trackpadMomentumMin    = 350.0
	trackpadMomentumStop   = 20.0
)

// wheelGlide is the notches of a wheel, or a touchpad's precise moves
// (fling), on their way through a scroll container: x and y go from 0 to
// as far as all of them scroll, and done is how far the content has
// scrolled with them so far. It moves the content by what it went since
// the last frame, so that what moves the content meanwhile, as a List
// measuring its rows, does not throw it off. reveal tells a
// ScrollIntoView gliding there instead (glideScroll).
type wheelGlide struct {
	x, y         channel
	doneX, doneY float64
	reveal       bool
	// last is when the last notch came, and streak counts the notches
	// that came fast enough to build up a speed. coast tells that they
	// stopped and the content goes on at that speed, decaying, from where
	// they left it: coastVX and coastVY are that speed, coastX and coastY
	// how far it has carried the content, and coastDoneX and coastDoneY
	// how far of that the content has moved. interval is how long the
	// notches took between them, so that a faster wheel coasts sooner. A
	// fling's precise moves follow the fingers at once; tvx and tvy are
	// the speed they built up, which its coast starts from.
	last       time.Time
	interval   time.Duration
	streak     int
	fling      bool
	tvx, tvy   float64
	coasted    bool
	coastAt    time.Time
	coastVX    float64
	coastVY    float64
	coastX     float64
	coastY     float64
	coastDoneX float64
	coastDoneY float64
}

// ahead returns how far the notches of the glide have yet to scroll s,
// as scrollBy takes them.
func (g *wheelGlide) ahead() (dx, dy float64) {
	if g == nil || g.reveal {
		return 0, 0
	}
	dx, dy = g.x.target-g.doneX, g.y.target-g.doneY
	if g.coasted {
		// What the coast has yet to carry the content.
		tau := g.coastTau()
		dx += g.coastX - g.coastDoneX + g.coastVX*tau
		dy += g.coastY - g.coastDoneY + g.coastVY*tau
	}
	return dx, dy
}

// coastTau returns the time constant of g's coast.
func (g *wheelGlide) coastTau() float64 {
	if g.fling {
		return trackpadMomentumTau.Seconds()
	}
	return wheelMomentumTau.Seconds()
}

// stopSpeed returns the speed at which g's coast is at rest.
func (g *wheelGlide) stopSpeed() float64 {
	if g.fling {
		return trackpadMomentumStop
	}
	return wheelMomentumStop
}

// wheelMoves reports whether a notch scrolling dx, dy moves container s
// further than the notches on their way there already do.
func wheelMoves(s *state, dx, dy float32) bool {
	ax, ay := s.wheel.ahead()
	if s.rtl {
		dx, ax = -dx, -ax
	}
	mx, my := max(0, s.contentW-float64(s.w)), max(0, s.contentH-float64(s.h))
	if dy != 0 && s.flags&flagScrollY != 0 {
		at := max(0, min(s.scrollY+ay, my))
		if max(0, min(at+float64(dy), my)) != at {
			return true
		}
	}
	if dx != 0 && s.flags&flagScrollX != 0 {
		at := max(0, min(s.scrollX+ax, mx))
		if max(0, min(at+float64(dx), mx)) != at {
			return true
		}
	}
	return false
}

// wheelScroll scrolls container s by a notch of dx, dy smoothly, on top
// of the notches on their way there.
func (rt *engine) wheelScroll(s *state, dx, dy float32) {
	now := rt.now()
	g := s.wheel
	if g == nil || g.reveal || g.fling {
		// The user takes over from ScrollIntoView, or from a trackpad's
		// fling, too.
		g = &wheelGlide{x: channel{at: now}, y: channel{at: now}, last: now}
		s.wheel = g
		if !slices.Contains(rt.wheeling, s.id) {
			rt.wheeling = append(rt.wheeling, s.id)
		}
	}
	if s.track != nil {
		s.track.glide = nil // the user takes over from ScrollTo
	}
	if l := s.list; l != nil {
		l.scrolled = false // and from the list's
	}
	g.x.advance(now)
	g.y.advance(now)
	g.notch(now)
	g.stopCoast()
	if dx != 0 {
		g.x.retarget(wheelTurn(&g.x, dx, float64(s.scrollX), &g.doneX), wheelAnimation)
	}
	if dy != 0 {
		g.y.retarget(wheelTurn(&g.y, dy, float64(s.scrollY), &g.doneY), wheelAnimation)
	}
	rt.requestFrame()
}

// trackpadScroll follows a precise move of dx, dy, keeping s's fling up to
// date with how fast the fingers move: the move itself is already on
// screen, and the fling goes on from it once they stop.
func (rt *engine) trackpadScroll(s *state, old *wheelGlide, dx, dy float32) {
	now := rt.now()
	g := old
	if g == nil || !g.fling || g.reveal {
		g = &wheelGlide{fling: true, x: channel{at: now}, y: channel{at: now}, last: now}
	}
	g.trackpad(now, dx, dy)
	s.wheel = g
	if !slices.Contains(rt.wheeling, s.id) {
		rt.wheeling = append(rt.wheeling, s.id)
	}
	rt.requestFrame()
}

// notch counts a notch at now among those that came fast enough to
// build up a speed.
func (g *wheelGlide) notch(now time.Time) {
	if g.last.IsZero() {
		g.last = now
		return
	}
	dt := now.Sub(g.last)
	g.last = now
	if dt <= 0 {
		// Notches of the same moment add up in the glide; there is no
		// way to tell how fast they came.
		return
	}
	if dt > wheelMomentumWindow {
		// A slow wheel, a notch at a time: it does not coast.
		g.streak = 0
		g.interval = 0
		return
	}
	g.streak++
	g.interval = dt
}

// coast scrolls g on past the notches as they stop, at the speed they
// built up, falling off as wheelMomentumTau says.
func (g *wheelGlide) coast(now time.Time) {
	if g.reveal || g.fling || g.coasted || g.streak < 2 || g.last.IsZero() {
		return
	}
	// A wheel turned at a steady pace must not coast between its notches:
	// wait longer than they took to come.
	gap := max(wheelMomentumGap, g.interval*3/2)
	switch idle := now.Sub(g.last); {
	case idle < gap, idle > wheelMomentumHold:
		return
	}
	g.coasted = true
	// The speed of the notches at the moment they stop: the momentum
	// starts from it, so that it does not jump.
	vx, vy := g.x.velocity, g.y.velocity
	if math.Abs(vx) < wheelMomentumMin {
		vx = 0
	}
	if math.Abs(vy) < wheelMomentumMin {
		vy = 0
	}
	if vx == 0 && vy == 0 {
		return
	}
	// The coast goes as far as the speed carries it in a tau; beyond
	// that it would throw the content too far.
	limit := wheelMomentumMax / wheelMomentumTau.Seconds()
	g.coastVX, g.coastVY = max(-limit, min(vx, limit)), max(-limit, min(vy, limit))
	g.coastX, g.coastY, g.coastDoneX, g.coastDoneY = 0, 0, 0, 0
	g.coastAt = now
	// What the notches had still to travel, the coast takes over.
	g.x.halt()
	g.y.halt()
}

// trackpad records a precise move of dx, dy at now, taking over where the
// last precise move left off: a fling starts from their speed when they
// stop.
func (g *wheelGlide) trackpad(now time.Time, dx, dy float32) {
	g.stopCoast()
	dt := now.Sub(g.last)
	g.last = now
	if dt <= 0 {
		return
	}
	if dt > trackpadMomentumWindow {
		g.tvx, g.tvy = 0, 0
		g.interval = 0
		g.streak = 0
		return
	}
	g.streak++
	g.interval = dt
	if dx != 0 {
		g.tvx = trackpadSpeed(g.tvx, float64(dx)/dt.Seconds())
	} else {
		g.tvx = 0
	}
	if dy != 0 {
		g.tvy = trackpadSpeed(g.tvy, float64(dy)/dt.Seconds())
	} else {
		g.tvy = 0
	}
}

// trackpadSpeed smooths a precise move's speed into prev, starting over
// as the fingers turn back.
func trackpadSpeed(prev, speed float64) float64 {
	if prev != 0 && (prev > 0) != (speed > 0) {
		return speed
	}
	return prev + (speed-prev)/2
}

// startFling starts g's coast from the speed its precise moves built up.
func (g *wheelGlide) startFling(now time.Time) {
	g.coasted = true
	limit := trackpadMomentumMax / trackpadMomentumTau.Seconds()
	g.coastVX = max(-limit, min(g.tvx, limit))
	g.coastVY = max(-limit, min(g.tvy, limit))
	g.coastX, g.coastY, g.coastDoneX, g.coastDoneY = 0, 0, 0, 0
	g.coastAt = now
	g.x.halt()
	g.y.halt()
}

// flingSpeed returns how fast g's precise moves are going.
func (g *wheelGlide) flingSpeed() float64 {
	return max(math.Abs(g.tvx), math.Abs(g.tvy))
}

// stopCoast ends the coast of g, as a notch starting over does.
func (g *wheelGlide) stopCoast() {
	if !g.coasted {
		return
	}
	g.coasted = false
	g.coastVX, g.coastVY = 0, 0
	g.coastX, g.coastY, g.coastDoneX, g.coastDoneY = 0, 0, 0, 0
}

// step moves g's content by what its notches and coast went since the
// last step, to now.
func (g *wheelGlide) step(now time.Time) (dx, dy float64) {
	dx, dy = g.x.value-g.doneX, g.y.value-g.doneY
	g.doneX, g.doneY = g.x.value, g.y.value
	if !g.coasted {
		return dx, dy
	}
	dt := now.Sub(g.coastAt).Seconds()
	g.coastAt = now
	if dt > 0 {
		tau := g.coastTau()
		decay := math.Exp(-dt / tau)
		g.coastX += g.coastVX * tau * (1 - decay)
		g.coastY += g.coastVY * tau * (1 - decay)
		g.coastVX, g.coastVY = g.coastVX*decay, g.coastVY*decay
		stop := g.stopSpeed()
		if math.Abs(g.coastVX) < stop {
			g.coastVX = 0
		}
		if math.Abs(g.coastVY) < stop {
			g.coastVY = 0
		}
	}
	dx += g.coastX - g.coastDoneX
	dy += g.coastY - g.coastDoneY
	g.coastDoneX, g.coastDoneY = g.coastX, g.coastY
	return dx, dy
}

// idle reports whether g has nowhere left to go.
func (g *wheelGlide) idle() bool {
	if g.x.moving || g.x.pending || g.y.moving || g.y.pending {
		return false
	}
	if g.coasted {
		return g.coastVX == 0 && g.coastVY == 0
	}
	return true
}

// wheelTurn returns the target for a notch of d on ch, scrolled to actual:
// when the notch goes the other way than the remaining travel, the
// remaining travel stops, and the notch starts over from where the content
// is, so that reversing the wheel at the end of the content turns at once
// instead of being absorbed by the notches still on their way.
func wheelTurn(ch *channel, d float32, actual float64, done *float64) float64 {
	if ch.moving && (d > 0) != (ch.target > ch.value) {
		ch.value = actual
		ch.velocity = 0
		*done = actual
		return actual + float64(d)
	}
	return ch.target + float64(d)
}

// glideWheels moves the containers that a wheel's notches or a touchpad's
// precise moves scroll, or ScrollIntoView glides, to the frame's time.
func (rt *engine) glideWheels(now time.Time) {
	kept := rt.wheeling[:0]
	for _, id := range rt.wheeling {
		s := rt.states[id]
		if s == nil || s.wheel == nil {
			continue
		}
		if t := s.track; t != nil && (t.X != float32(s.scrollX) || t.Y != float32(s.scrollY) || t.glide != nil) ||
			s.list != nil && s.list.scrolled {
			// The app set the offset, or a ScrollTo, of its ScrollState
			// or its ListState: it takes over.
			s.wheel = nil
			continue
		}
		g := s.wheel
		g.x.advance(now)
		g.y.advance(now)
		if g.fling && !g.coasted {
			// The fingers left: wait until they have been still long
			// enough to tell a stop from the next move.
			idle := now.Sub(g.last)
			if idle < trackpadMomentumGap {
				rt.animating = true
				kept = append(kept, id)
				continue
			}
			if idle > trackpadMomentumHold || g.flingSpeed() < trackpadMomentumMin {
				s.wheel = nil
				continue
			}
			g.startFling(now)
		}
		g.coast(now)
		dx, dy := g.step(now)
		moved := scrollBy(s, float32(dx), float32(dy))
		if !moved && (dx != 0 || dy != 0) || g.idle() {
			// At the end of the content, or there.
			s.wheel = nil
			if g.reveal && s.snap != nil {
				// It rests at the place nearest.
				s.snap.scrolled(rt, s, 0)
			}
			continue
		}
		rt.animating = true
		kept = append(kept, id)
	}
	rt.wheeling = kept
}

// glideScroll scrolls container s smoothly to offset x, y as a says, as
// a wheel's notches glide (ScrollIntoView with an Animation): the user
// scrolling, or the app, takes over.
func (rt *engine) glideScroll(s *state, x, y float64, a Animation, now time.Time) {
	dx, dy := x-s.scrollX, y-s.scrollY
	if dx == 0 && dy == 0 {
		if s.wheel != nil && s.wheel.reveal {
			s.wheel = nil // there already
		}
		return
	}
	if s.rtl {
		dx = -dx // as scrollBy takes it
	}
	if g := s.wheel; g != nil && g.reveal && math.Abs(g.x.goal()-g.doneX-dx) < snapEps && math.Abs(g.y.goal()-g.doneY-dy) < snapEps {
		// On its way there already, as when asked again.
		rt.animating = true
		return
	}
	g := &wheelGlide{x: channel{at: now}, y: channel{at: now}, reveal: true}
	headFor(&g.x, dx, a, now)
	headFor(&g.y, dy, a, now)
	s.wheel = g
	if !slices.Contains(rt.wheeling, s.id) {
		rt.wheeling = append(rt.wheeling, s.id)
	}
	if s.track != nil {
		s.track.glide = nil // it takes over from ScrollTo
	}
	if l := s.list; l != nil {
		l.scrolled = false // and from the list's
	}
	s.haltSnap()
	rt.animating = true
}
