package api

import "net/http"

// serverInfoResponse is the live effective server config the frontend needs to detect
// drift against previously-stored state (e.g. an app-sync connection's HarbrrURL baked
// in against a since-changed port).
type serverInfoResponse struct {
	Port int `json:"port"`
}

// serverInfo returns harbrr's configured listening port. It intentionally reports the
// configured value, not anything derived from the inbound request (a reverse proxy or
// port-forward would make that misleading) — see the AGENTS.md scope note for why
// scheme/host are deliberately out of scope.
func (rt *router) serverInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, serverInfoResponse{Port: rt.cfg.Port})
}
