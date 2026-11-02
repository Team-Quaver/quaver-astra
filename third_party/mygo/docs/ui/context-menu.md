# Context menu

<!-- preview context-menu 180 -->

`ContextMenu` gives an element a menu of the system's, which opens where
the element is right-clicked (Control-clicked on macOS), and below it when
the menu key or Shift+F10 is pressed while it or one inside it has the
focus. The function builds the items when the menu opens, and runs again in
the frame after one was chosen, where its `Chosen` reports it:

```go
row.ContextMenu(func(m *ui.Menu) {
	if m.Item("Rename").Shortcut(0, ui.KeyF2).Chosen() {
		app.renaming = i
	}
	if m.Item("Pinned").Checked(note.pinned).Chosen() {
		note.pinned = !note.pinned
	}
	m.Submenu("Move to", func(m *ui.Menu) {
		for _, f := range app.folders {
			if m.Item(f.name).Chosen() {
				moveTo, moved = f, i
			}
		}
	})
	m.Separator()
	if m.Item("Delete").Disabled(note.locked).Chosen() {
		deleted = i
	}
})
```

The function runs as the element builds, in the loop building the rows:
a choice that takes the row out of the notes, as moving or deleting it,
notes it, for the change once the loop is done (see
[views](views.md#events-and-actions)).

## Items

- `Item(label)` adds an item; `Chosen` reports whether it was chosen.
- `Checked(on)` shows a check mark by it, as for a setting that choosing the
  item toggles, and `Radio(on)` shows it as one of a group choosing one
  setting, marked as the one chosen when on is true.
- `Disabled(d)` grays it out: it cannot be chosen.
- `Shortcut(mods, key)` shows a key by it, as the shortcut that does the
  same. It only shows the key: handle it with `Shortcut` on the context or
  an element.
- `Separator()` adds a line between groups of items, and
  `Submenu(label, build)` an item opening a submenu, which `build` fills.

## Which element

The innermost element with a menu gets the click, as a row's button with a
menu of its own inside a row with another. [Text inputs](text-input.md)
have the editing commands of their platform's text fields, and selectable
[text](text.md) has Copy and Select All. `ContextMenu` on the input, the
text, or the `Selectable` container replaces that menu, and `EditItems`
adds those commands to a menu of one's own, acting on the text that was
clicked, as a chat message's text offers Reply above Copy:

```go
message.Selectable().ContextMenu(func(m *ui.Menu) {
	if m.Item("Reply").Chosen() {
		app.reply(msg)
	}
	m.Separator()
	m.EditItems()
})
```

The same menu opens below a button with `Element.Menu`: see
[menu button](menu-button.md). In tests, `tt.RightClick` opens a context
menu, which `tt.Menu` lists and `tt.ChooseMenuItem` chooses from.

## Drawn in the window

`PopoverMenu` takes the same function, and MyGo draws the menu in the
window with the theme's look, as a [popover](popover.md), where the
system would show its own: beside the popovers and buttons of an app's own
design, the system's menu is the one that looks out of place.

```go
card.PopoverMenu(func(m *ui.Menu) {
	if m.Item("Play Next").Chosen() {
		app.playNext(album)
	}
	if m.Item("Pinned").Checked(album.pinned).Chosen() {
		album.pinned = !album.pinned
	}
	m.Submenu("Sort by", func(m *ui.Menu) {
		for _, by := range []string{"Title", "Artist", "Year"} {
			if m.Item(by).Radio(app.sort == by).Chosen() {
				app.sort = by
			}
		}
	})
	m.Separator()
	if m.Item("Remove").Shortcut(0, ui.KeyDelete).Disabled(album.locked).Chosen() {
		app.remove(album)
	}
})
```

It opens as the system's would, where the element is right-clicked, or
below it for the menu key or Shift+F10, after that point, or before it
where the window has no room, and follows the element as the window
scrolls. It takes the focus as it opens and gives it back as it closes to
the element that had it, and has the keys of the system's menus:

- Up and Down move among the items, past separators and those disabled,
  and round; Home and End go to the first and the last, and typing the
  first letters of an item goes to it.
- Right opens the submenu of the item, and Left closes it, the other way
  round [right to left](right-to-left.md); the pointer opens it as it
  moves onto the item.
- Enter and Space choose the item, as a click does; Escape closes the
  submenu on top, then the menu, and Tab closes the menu.
- A press outside the menu closes it, and goes on to what is under the
  pointer.

Its items show their `Shortcut` after their label, with a check mark or
a dot by those `Checked` or `Radio` marks. Assistive technology sees a
menu of menu items, check box items and radio items, checked or not, as
it sees the system's.

A drawn menu's items are elements: in tests, `tt.RightClick` opens it,
`tt.HasText` finds its items and `tt.Click` chooses one, while `tt.Menu`
lists only the system's menus.

`PopoverMenuButton` draws the menu of a [menu
button](menu-button.md#drawn-in-the-window) so, below the button, and takes
the same function: a row's right-click and its ⋯ button can share one
menu, drawn alike.
