# Scroll view

<!-- preview scroll 280 -->

`ui.Scroll` and `ui.ScrollHorizontal` scroll their children with the wheel,
the touchpad, their scroll bar and the keyboard: the arrow keys, Page Up and
Page Down, Space, Home and End scroll the container around the focus, or
under the pointer, unless the focused element takes those keys, as a text
input does. `ui.ScrollBoth` scrolls both ways, as a canvas or a wide table
does. A notch of a mouse wheel glides over a short ease out, as browsers
scroll it, and notches in a row add up, until the app scrolls the
container (a `ScrollState`, a `ListState`'s `ScrollTo`, `ScrollIntoView`
and `ScrollToEnd`, or `ScrollIntoView` of an element in it), which takes
over; a touchpad follows the fingers, as a finger dragged on a
touchscreen does on Linux, and while the desktop asks for less
motion, a notch scrolls at once.

```go
ui.Scroll(c).Grow(1).Padding(16).Gap(12).Children(func() {
	for _, p := range app.posts {
		post(c, p)
	}
})
```

Give them a size, or grow them in their parent. For the rows of a
collection, use a [list](list.md), which builds only those in view.

Laid out [right to left](right-to-left.md), a container scrolling sideways
shows the start of its content, at the right, first, and `ScrollState.X`
counts from there: 0 is the start and `MaxX` the end, as Flutter's offsets
are.

## Keeping the offset

A scroll container keeps its offset as it keeps its other state, by its
place in the view or its `Key`. To read the offset, set it or keep it in
the app's state, give the container a `ui.ScrollState` with `TrackScroll`:
the container shows its content from the state's `X` and `Y`, writes them
as the user scrolls, and sets `MaxX` and `MaxY` to how far they go. `Y = 0`
scrolls to the top and `math.MaxFloat32` to the end, and a `ScrollState`
for each page a container shows keeps each page's place. A log that follows
its end, unless the user scrolled up from it:

```go
if app.log.Y >= app.log.MaxY {
	app.log.Y = math.MaxFloat32
}
ui.Scroll(c).TrackScroll(&app.log).Grow(1).Children(app.lines)
```

`ScrollTo(x, y, animation)` scrolls there smoothly, as a spring or a curve
(see [Motion](motion.md)), turning toward a new place if called again on
the way; the user scrolling stops it, and `Scrolling()` reports whether it
is on its way. Where the desktop asks for less motion, it scrolls at once:

```go
if ui.Button(c, "Back to top").Clicked() {
	app.feed.ScrollTo(0, 0, ui.Smooth)
}
```

## Scrolling into view

`ScrollIntoView` scrolls the containers around an element as little as
shows it, once the frame is laid out, so that it works in the frame that
adds the element: a new message, or the item the keys chose. Call it in that
frame only, or the user could not scroll the element away. Rows a `List`
has not built scroll into view with its `ListState`.

```go
if app.added {
	ui.Text(c, msg.Text).ScrollIntoView()
}
```

The keyboard focus scrolls into view as it moves. An element with a
`TextInputClient` of the app's ([text input clients](text-input-client.md))
scrolls its caret into view, where `BoundsForRange` places it, rather than
its whole box: a code editor as high as its lines shows the line being
edited as it takes the focus, not its first.

## Scroll bars

A scroll container whose content overflows it shows the thumbs of its
scroll bars over its content while the pointer is over it, which the user
drags; they take no room. Right to left, the vertical bar is on the left. The theme's `ScrollbarWidth` sets their width and
its `Scrollbar` their color.

`ScrollbarInsets` moves the bars in from the container's edges, CSS style
as `Padding`: the vertical bar runs from the top inset to the bottom one,
the right inset in from the right, and the horizontal bar from the left
inset to the right one, the bottom inset up. So the bars keep clear of
what floats over the content, as a toolbar it scrolls under, as AppKit's
and UIKit's do:

```go
ui.Scroll(c).Fill().Padding(64, 16, 16).ScrollbarInsets(64, 0, 0).Children(func() { /* ... */ })
// The toolbar floats over the top 64 DIPs.
ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(64).Children(func() { /* ... */ })
```

`Scrollbars(false)` takes the bars away, and the presses on them, from a
container that draws its own chrome or shows where it is by itself, as a
carousel or a map: the wheel, `TrackScroll`, the keyboard and assistive
technology still scroll it.

An element that scrolls itself rather than through a container, as the
code editor and the terminal do, draws its bars with `ui.Scrollbar`, so
that they look as the containers' do and follow the theme: `Painter.Scrollbar`
paints the thumb, and `Thumb` and `OffsetAt` map a drag of it to an
offset. Show it as a container does, while the
pointer is over the element or drags the thumb:

```go
bar := ui.Scrollbar{Box: r, View: r.H, Content: contentH, Offset: scrollY}
if hovered || dragging {
	p.Scrollbar(bar, ui.ScrollbarState{Dragging: dragging})
}
```

An element inside that zooms or pans with the wheel, the touchpad and
pinches, as a chart, takes them from the container: see
[Scrolling and pinching](input.md#scrolling-and-pinching).

## Accessibility

Assistive technology sees a scroll area, which it scrolls to show what it
reads.
