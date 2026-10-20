package mygo

// Page is the web page a window shows: what it loads, the scripts it runs,
// its developer tools and its events. A window showing native UI
// (WindowOptions.Content) has none.
//
//	win := mygo.NewWindow(mygo.WindowOptions{Title: "Editor", URL: "/"})
//	win.Page().OnDOMReady(func() { log.Println("ready") })
//
// Like the window's, its methods are safe from any goroutine.
type Page struct{ w *Window }

// Page returns the web page the window shows, or nil for a window that
// shows native UI.
func (w *Window) Page() *Page {
	if w.content != nil {
		return nil
	}
	return w.pg
}

// Window returns the window that shows the page: for the page of a web
// view, the window the web view is in.
func (p *Page) Window() *Window { return p.w.top() }

// WebView returns the web view whose page this is, nil for the page of a
// window.
func (p *Page) WebView() *WebView { return p.w.webView }

// PageOptions configure the web page of a window.
type PageOptions struct {
	// PreloadScript is JavaScript injected into every page before the
	// page's own scripts, after window.mygo is available.
	PreloadScript string
	// TrustedOrigins lists extra origins, such as "https://example.com",
	// whose pages may call bound Go methods. By default only the app's own
	// content can: custom schemes registered with Protocol, file: and
	// about: pages, and loopback dev servers during development. "*"
	// trusts every origin.
	TrustedOrigins []string
	// DevTools controls the web inspector, and for a window showing
	// Content, the inspector of its native UI.
	DevTools DevTools
	// ZoomFactor of the page (default 1).
	ZoomFactor float64
	// UserAgent overrides the user agent string.
	UserAgent string
	// SafariTabFocus leaves Tab to text fields and elements with a
	// tabindex, as Safari does while the system's keyboard navigation
	// (System Settings › Keyboard › Keyboard navigation) is off (macOS).
	// By default Tab reaches links, buttons, checkboxes and selects too,
	// as it does in Chrome, Electron and WebView2.
	SafariTabFocus bool
	// UserAgentSuffix appends to the page's user agent instead of
	// replacing it, as UserAgent does: the app's name and version, such as
	// "MyApp/1.2", for the sites that read them. It appends to UserAgent
	// too, which SetUserAgent keeps as well; leading and trailing spaces
	// are trimmed. Some sites serve a bare web view, whose user agent
	// names no browser, older pages: "Version/17.0 Safari/605.1.15" makes the
	// page look like Safari.
	UserAgentSuffix string
}
