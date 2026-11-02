# Automation

The automation plugin (`plugins/automation`) lets another process drive a
running app: an end-to-end test runner or an agent lists its windows,
reads what their native UI shows as elements, acts on them, sends
pointer, wheel, key and text input, chooses items of the application
menu, evaluates scripts in pages, captures windows as PNG images, and
waits for them to settle. It speaks one protocol on macOS, Linux and
Windows: newline-delimited JSON over a Unix domain socket.

Elements are the ones MyGo gives assistive technology, so a client sees
what a screen reader sees and acts as one does, with the IDs of the
engine and the `Key`s the app gave elements. [ui.Tester](../ui/testing.md)
remains the way to test a view inside `go test`; this plugin is for the
built app, from outside.

## Turning it on

An app opts in by importing the package. Keep it out of the app's
release with a build tag, in a file of its own:

```go
//go:build e2e

package main

import _ "github.com/egoist/mygo/plugins/automation"
```

```sh
go build -tags e2e -o app .
MYGO_AUTOMATION=/tmp/app.sock ./app
```

It listens only when `MYGO_AUTOMATION` names the path of the socket, and
does nothing otherwise. Builds without the inspector of native UI leave
it out, as they do the inspector: `go build -tags mygo_noinspector` and
the release builds of `mygo build`, unless `-debug` or
`MYGO_INSPECTOR=1` keeps the inspector. Such a build that
imports it ignores `MYGO_AUTOMATION`, and says so on its standard error.

A socket left at the path by an app that ended without closing it is
replaced; one that another app listens on is not, nor a file that is no
socket. The app removes its socket when it quits.

## The protocol

A client connects to the socket and writes requests, one JSON object per
line. The app answers each on a line of its own, in order, each once the
one before it is answered; clients that want requests to run side by
side, as `idle` with one window while another acts, open more
connections.

```json
{"id": 1, "method": "tree", "params": {"window": 1}}
```

`id` is any JSON value, which the answer repeats. An answer has a
`result`, or an `error` with a `code` to test and a `message` to read:

```json
{"id": 1, "result": {"window": 1, "nodes": [...]}}
{"id": 2, "error": {"code": "no_window", "message": "no window 7"}}
```

| Code | Meaning |
| --- | --- |
| `parse_error` | The line is not a JSON object (its `id` is then `null`). |
| `invalid_request` | No method, params the method does not take, or values it cannot use. |
| `unknown_method` | No such method. |
| `no_window` | No window has the ID, or the app has none. |
| `no_element` | No element of the window's last frame has the ID or the key. |
| `unsupported` | The window cannot do it, as reading the elements of a page. |
| `timeout` | `idle` gave up after its `timeout`. |
| `failed` | The app could not do it, as a script that threw. |

Every method but `windows` takes a `window`, the ID `windows` lists; left
out, it is the focused window, else the first. Coordinates and sizes are
in DIPs, relative to the window's content, as the elements' bounds are.

| Method | Params | Result |
| --- | --- | --- |
| `windows` | | `{windows: [{id, title, kind, url?, bounds, contentBounds, focused, visible}]}` |
| `tree` | `window` | `{window, focus?, nodes: [...], announcements?}` |
| `act` | `window`, `id` or `key`, `action`, `text?`, `from?`, `to?` | `{}` |
| `input` | `window`, `events` | `{}` |
| `menu` | `window`, `path` | `{}` |
| `capture` | `window` | `{png, width, height}` |
| `eval` | `window`, `script` | `{value}` |
| `idle` | `window`, `timeout?` | `{}` |

### windows

Lists the open windows. `kind` is `content` for a window of native UI and
`page` for one showing a web page, whose `url` it gives; `bounds` is the
window's frame on screen and `contentBounds` its content's, each `{x, y,
width, height}`.

```json
{"id": 1, "method": "windows"}
{"id": 1, "result": {"windows": [{"id": 1, "title": "Counter", "kind": "content", "bounds": {"x": 440, "y": 362, "width": 400, "height": 300}, "contentBounds": {"x": 440, "y": 387, "width": 400, "height": 275}, "focused": true, "visible": true}]}}
```

### tree

The elements of the window's last frame, in tree order, a parent before
its children:

```json
{"id": 2, "method": "tree", "params": {"window": 1}}
{"id": 2, "result": {"window": 1, "focus": "5878896818624359798", "nodes": [
  {"id": "15956256791619451359", "role": "text", "label": "Count: 0", "bounds": {"x": 20, "y": 20, "width": 360, "height": 18}},
  {"id": "5424175759328386547", "role": "button", "key": "add", "label": "Add", "bounds": {"x": 20, "y": 48, "width": 360, "height": 32}, "states": ["focusable"], "actions": ["press", "focus"]},
  {"id": "5878896818624359798", "role": "textField", "key": "name", "label": "Name", "value": "Ada", "bounds": {"x": 20, "y": 90, "width": 360, "height": 32}, "states": ["focusable"], "actions": ["focus", "setValue"], "selection": [3, 3]}
]}}
```

A node has its `id`, the `parent`'s ID unless it is at the top, its
`role`, and those of `key`, `label`, `value`, `placeholder`,
`description`, `bounds`, `states`, `actions`, `min`, `max`, `now` and
`step` (of sliders, progress bars, meters and steppers), `selection` (of
text fields, in runes), `level`, `posInSet` and `setSize` that it has.
IDs are strings of digits: they are 64-bit, which JavaScript's numbers
would round.

`key` is the element's `Key`, given with `Element.Key` or, for widgets
that take theirs before they are made, `Context.Key`: a string, a
boolean or an integer as itself, another value as Go prints it. Unlike
an ID, which a frame may change as the view changes, it is the app's
own name for the element, made to last; give the elements tests look
for keys of their own:

```go
c.Key("name")
ui.TextInput(c, &name).Label("Name")
ui.Button(c, "Add").Key("add")
```

Only the elements assistive technology reads are in the tree: a row or
a column that lays others out has no node, keyed or not. Roles are
`group`, `text`, `button`, `link`, `checkBox`, `radio`, `switch`,
`slider`, `progress`, `textField`, `image`, `list`, `scroll`, `dialog`,
`popup`, `tooltip`, `popUpButton`, `tabList`, `tab`, `splitter`,
`status`, `table`, `row`, `cell`, `columnHeader`, `tree`, `treeItem`,
`listItem`, `menuButton`, `toolbar`, `radioGroup`, `toggleButton`,
`comboBox`, `disclosure`, `meter`, `stepper`, `colorWell`,
`alertDialog`, `menu`, `menuBar`, `menuItem`, `menuItemCheckBox`,
`menuItemRadio` and `heading`. States are `focusable`, `disabled`,
`checked`, `mixed`, `selected`, `expanded`, `multiline`, `password`,
`readOnly`, `selectable`, `offscreen`, `multiselectable`, `segment`,
`search`, `invalid`, `sortAscending`, `sortDescending`, `expandable` and
`vertical`.

A web view in native UI shows as a node marked `webView`, without the
elements of its page. A window showing a web page has no tree: `tree`
answers `unsupported`, and clients read the page with `eval`.

### act

Performs an action on an element, as assistive technology does: the
element is found by its `id`, or by its `key` when exactly one element
has it.

```json
{"id": 3, "method": "act", "params": {"window": 1, "key": "add", "action": "press"}}
{"id": 4, "method": "act", "params": {"window": 1, "key": "name", "action": "setValue", "text": "Grace"}}
```

| Action | Does |
| --- | --- |
| `press` | Clicks a button, checks a check box, opens a menu button. |
| `focus` | Gives the element the keyboard focus. |
| `increment`, `decrement` | Steps a slider or a stepper. |
| `setValue` | Replaces a text field's text with `text`. |
| `insertText` | Types `text` at a text field's caret. |
| `selectText` | Selects runes `from` to `to` of a text field. |
| `scrollIntoView` | Scrolls the containers around the element to show it. |
| `expand`, `collapse` | Opens or closes an item of a tree. |

### input

Sends events to the window's content through the path the system's take,
in order:

```json
{"id": 5, "method": "input", "params": {"window": 1, "events": [
  {"type": "click", "x": 200, "y": 99},
  {"type": "text", "text": "Grace"},
  {"type": "key", "key": "CmdOrCtrl+A"}
]}}
```

| Type | Fields |
| --- | --- |
| `pointerMove` | `x`, `y`, `mods?` |
| `pointerDown`, `pointerUp` | `x`, `y`, `button?`, `clicks?`, `mods?` |
| `click` | `x`, `y`, `button?`, `clicks?`, `mods?`: a move, then a press and a release for each click |
| `wheel` | `x`, `y`, `dx`, `dy`, `precise?`, `mods?`: scrolls by `dx`, `dy` DIPs, as a touchpad when `precise` |
| `keyDown`, `keyUp`, `key` | `key`: a key and its modifiers as menu accelerators write them, as `Enter`, `Shift+Tab` or `CmdOrCtrl+Z`; `key` presses and releases it |
| `text` | `text`: text typed, or committed by an input method |

`button` is `left` (the default), `right` or `middle`; `clicks` counts
the presses of a double or triple click; `mods` lists `shift`, `ctrl`,
`alt`, `super` and `cmdOrCtrl`. A key event types no text: send the text
with a `text` event, as backends send both for a key that types.

The content handles the events of a request one after the other, before
it draws its next frame, as it would events that came together. When an
event depends on what an earlier one showed, as typing into a field a
click focused, wait with `idle` in between. Input goes to windows of
native UI; pages answer `unsupported`, and clients drive them with
`eval`.

### menu

Chooses an item of the application menu by the labels along its path, as
the menu bar does, and answers once the item's `Click` returned:

```json
{"id": 6, "method": "menu", "params": {"path": ["File", "New Window"]}}
```

On Linux and Windows it chooses from the window's menu bar, which shows
the application menu. The menus of the Dock and of tray icons, and open
context menus, are left out: the system shows a context menu in a loop
of its own, which the plugin cannot choose from on every platform, so
don't open one with a right click through `input`. Test them with
[ui.Tester](../ui/testing.md), whose `RightClick` and `ChooseMenuItem`
open and choose from the view's context menus.

### capture

The window as a PNG image, in base64, `width` × `height` device pixels:
native UI rendered as it is, or a page's screenshot.

### eval

Evaluates JavaScript in the page of a window showing one, as `Page.Eval`
does: an expression's value, with promises awaited, or what statements
`return`.

```json
{"id": 7, "method": "eval", "params": {"window": 2, "script": "document.title"}}
{"id": 7, "result": {"value": "Todo"}}
```

### idle

Answers once the window settled, so that clients wait for the UI rather
than sleep: for native UI, once the content asks for no frame it has not
drawn, as `Tester.FramePending` tells; for a page, once it has loaded and
answered a script. It waits for nothing that `Context.After` or
`Painter.After` asked for later, as a tester's frames don't. Content that
never settles, as an indicator that keeps moving, never answers: give
`timeout`, in milliseconds, to have it fail with `timeout` instead.

```json
{"id": 8, "method": "idle", "params": {"window": 1, "timeout": 5000}}
```

## A client

Any language that opens Unix domain sockets can be a client. A test in
Go:

```go
conn, err := net.Dial("unix", "/tmp/app.sock")
if err != nil {
	t.Fatal(err)
}
r := bufio.NewReader(conn)
call := func(method string, params any) json.RawMessage {
	line, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": params})
	conn.Write(append(line, '\n'))
	answer, _ := r.ReadBytes('\n')
	var res struct {
		Result json.RawMessage
		Error  *struct{ Code, Message string }
	}
	json.Unmarshal(answer, &res)
	if res.Error != nil {
		t.Fatalf("%s: %s", method, res.Error.Message)
	}
	return res.Result
}
call("act", map[string]any{"key": "add", "action": "press"})
call("idle", nil)
tree := call("tree", nil)
```

and in a shell, with `socat`:

```sh
echo '{"id": 1, "method": "windows"}' | socat - UNIX-CONNECT:/tmp/app.sock
```

## Security

Whoever opens the socket drives the app as its user would, and runs
scripts in its pages: keep the plugin out of the builds you ship. It is
opt-in twice: an app links it only by importing it, which a build tag
keeps to test builds, and listens only when `MYGO_AUTOMATION` is set.
Builds without the inspector leave it out whatever they import.

The socket is the user's alone: on macOS and Linux it is made with mode
`0600`, and on Windows, where a socket's file has no mode, it takes the
access control list of its directory, which is the user's alone in the
user's profile and temporary directory. Put it in a directory only the
user can write to, not one shared with others.

## Platforms

Windows need Windows 10 1803 or later, which has Unix domain sockets.
Browsers (`GOOS=js`) have no sockets: the plugin compiles there, and
listens nowhere.
