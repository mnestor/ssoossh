// The app ships as a single-page bundle embedded in the Go binary
// (server/frontend), so there is no Node process to render on and nothing to
// prerender against: every page's data comes from an authenticated API call
// made by the browser.
//
// adapter-static's fallback: 'index.html' is what makes deep links like
// /approve/<uuid> work — the Go server serves the shell for any unmatched
// path (see server/frontend/frontend_included.go) and the client router
// takes it from there.
export const ssr = false;
export const prerender = false;
