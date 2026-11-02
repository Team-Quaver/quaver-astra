# Menu button

<!-- preview menu-button 120 -->

`ui.MenuButton` creates a button showing a label and an arrow, which opens
a menu of the system's below it, as the system's pop-up buttons do: as the
pointer goes down on it, or for Enter, Space or Down while it has the
keyboard focus. The function builds the items when the menu opens, and runs
again in the frame after one was chosen, where its `Chosen` reports it:

```go
ui.MenuButton(c, "Sort by", func(m *ui.Menu) {
	for _, by := range []string{"Name", "Date", "Size"} {
		if m.Item(by).Checked(app.sort == by).Chosen() {
			app.sort = by
		}
	}
})
```

## Items

Menus take the same items as [context menus](context-menu.md#items):
`Item`, with `Checked`, `Radio`, `Disabled` and `Shortcut`, `Separator`
and `Submenu`.

## Any button

`Element.Menu` gives the same menu to any button, as one showing only an
icon:

```go
ui.Button(c, "").Label("More").Children(func() { ui.Icon(c, dots) }).Menu(func(m *ui.Menu) {
	if m.Item("Duplicate").Chosen() {
		app.duplicate()
	}
	if m.Item("Delete").Chosen() {
		app.delete()
	}
})
```

In a [toolbar](toolbar.md), a menu button takes no face of its own until
hovered.

## Drawn in the window

`ui.PopoverMenuButton` takes the same function, and MyGo draws the menu in
the window with the theme's look, as
[`PopoverMenu`](context-menu.md#drawn-in-the-window) draws a context menu,
where the system would show its own: beside the popovers and buttons of
an app's own design, the system's menu is the one that looks out of place,
and a right-click's drawn menu and a ⋯ button's system menu, side by
side, look like two menus. `Element.PopoverMenuButton` gives such a menu to
any button, as `Element.Menu` does, and takes the same function as
`PopoverMenu`, so one function serves both:

```go
menu := func(m *ui.Menu) {
	if m.Item("Rename").Shortcut(0, ui.KeyF2).Chosen() {
		app.renaming = i
	}
	if m.Item("Pinned").Checked(note.pinned).Chosen() {
		note.pinned = !note.pinned
	}
	m.Separator()
	if m.Item("Delete").Disabled(note.locked).Chosen() {
		app.delete(i)
	}
}
row.PopoverMenu(menu)
row.Children(func() {
	ui.Text(c, note.title).Grow(1)
	ui.Button(c, "").Label("More").Children(func() { ui.Icon(c, dots) }).PopoverMenuButton(menu)
})
```

It opens as the system's would, as the pointer goes down on the button,
and for Enter, Space or Down while the button has the focus: below the
button, lined up with its start, or above it where the window has no room.
Pressing the button again closes it. It has the keys, the focus and the
look of `PopoverMenu`'s: it takes the focus as it opens, on its first
item when the keyboard opened it, and gives it back to the button as it
closes; Up, Down, Home, End and typing move among its items, Right and
Left open and close submenus, Enter and Space choose, and Escape, Tab and
presses outside close it. A choice reports `Chosen` as from
`PopoverMenu`.

Assistive technology sees the menu button, expanded while its menu
shows, and the menu with its menu items, check box items and radio items,
as it sees the system's. In a toolbar's overflow menu, choosing the button
opens its drawn menu below the overflow button.

A drawn menu's items are elements: in tests, `tt.Click` on the button
opens it, `tt.HasText` finds its items and `tt.Click` chooses one, while
`tt.Menu` lists only the system's menus.

## Accessibility

Assistive technology sees a menu button: `AXMenuButton` on macOS, a push
button with a menu on Linux, and a button that expands on Windows.
