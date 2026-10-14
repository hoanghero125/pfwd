package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hoanghero125/pfwd/internal/ipc"
)

// maxBody caps a request body.
const maxBody = 1 << 20

// tokenHeader carries the session's token on API calls.
const tokenHeader = "X-PFWD-Token" //nolint:gosec // a header name, not a credential

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("/", s.static)

	api := http.NewServeMux()
	call := func(method string, params func(r *http.Request) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var p any
			if params != nil {
				var err error
				if p, err = params(r); err != nil {
					writeError(w, err)
					return
				}
			}
			s.call(w, r, method, p)
		}
	}
	host := func(r *http.Request) (any, error) { return map[string]string{"host": r.PathValue("host")}, nil }
	api.HandleFunc("GET /api/hosts", call("hosts", nil))
	api.HandleFunc("POST /api/hosts/{host}/connect", call("connect", func(r *http.Request) (any, error) {
		return map[string]string{"target": r.PathValue("host")}, nil
	}))
	api.HandleFunc("POST /api/hosts/{host}/disconnect", call("disconnect", host))
	api.HandleFunc("POST /api/hosts/{host}/reconnect", call("reconnect", host))
	api.HandleFunc("GET /api/hosts/{host}/ports", s.ports)
	api.HandleFunc("PATCH /api/hosts/{host}/settings", call("set_auto", func(r *http.Request) (any, error) {
		var body struct {
			Auto *bool `json:"auto"`
		}
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		if body.Auto == nil {
			return nil, ipc.Errorf(ipc.CodeUsage, "nothing to change: send {\"auto\": true} or false")
		}
		return map[string]any{"host": r.PathValue("host"), "on": *body.Auto}, nil
	}))
	api.HandleFunc("POST /api/forwards", call("forward", body))
	api.HandleFunc("DELETE /api/forwards/{id}", call("stop", func(r *http.Request) (any, error) {
		return map[string][]string{"ids": {r.PathValue("id")}}, nil
	}))
	api.HandleFunc("PATCH /api/forwards/{id}", s.patchForward)
	api.HandleFunc("GET /api/config", call("config_get", nil))
	api.HandleFunc("PUT /api/config", call("config_put", body))
	api.HandleFunc("PATCH /api/config", call("config_set", body))
	api.HandleFunc("POST /api/prompts/{id}", call("answer", func(r *http.Request) (any, error) {
		var m map[string]any
		if err := readJSON(r, &m); err != nil {
			return nil, err
		}
		m["id"] = r.PathValue("id")
		return m, nil
	}))
	api.HandleFunc("GET /api/events", s.events)

	mux.Handle("/api/", s.authorized(api))
	mux.HandleFunc("GET /api/session", s.sessionInfo)
	return mux
}

// body passes the request's JSON body on as the call's parameters.
func body(r *http.Request) (any, error) {
	var raw json.RawMessage
	if err := readJSON(r, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func readJSON(r *http.Request, v any) error {
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
	if err != nil {
		return ipc.Errorf(ipc.CodeUsage, "read the request: %v", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return ipc.Errorf(ipc.CodeUsage, "the request body is not the JSON this call takes: %v", err)
	}
	return nil
}

// authorized lets through API calls with a session cookie and the
// session's token.
func (s *Server) authorized(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		sess, ok := s.session(r)
		if !ok {
			writeStatus(w, http.StatusUnauthorized, "login", "This page isn't logged in. Run `pfwd ui` for a new login link.")
			return
		}
		got := r.Header.Get(tokenHeader)
		if r.URL.Path == "/api/events" {
			// Browsers can't set headers on a WebSocket.
			got = r.URL.Query().Get("token")
		}
		if !s.tokenOK(sess, got) {
			writeStatus(w, http.StatusForbidden, "login", "This tab has no pfwd login. Run `pfwd ui` for a new login link.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sessionInfo tells the page its session's token, the first time it asks.
func (s *Server) sessionInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := s.session(r)
	if !ok {
		writeStatus(w, http.StatusUnauthorized, "login", "This page isn't logged in. Run `pfwd ui` for a new login link.")
		return
	}
	info := map[string]string{"version": s.o.Version}
	// A page with a token only checks it; a page without one takes one.
	if got := r.Header.Get(tokenHeader); got != "" {
		if !s.tokenOK(sess, got) {
			writeStatus(w, http.StatusForbidden, "login", "This tab's pfwd login has ended. Run `pfwd ui` for a new login link.")
			return
		}
	} else if token, ok := s.claim(sess); ok {
		info["token"] = token
	} else {
		writeStatus(w, http.StatusForbidden, "login", "This tab has no pfwd login. Run `pfwd ui` for a new login link.")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// call runs one backend call and writes its result.
func (s *Server) call(w http.ResponseWriter, r *http.Request, method string, params any) {
	raw, err := json.Marshal(params)
	if err != nil {
		writeError(w, err)
		return
	}
	if params == nil {
		raw = nil
	}
	res, err := s.b.Handle(r.Context(), method, raw)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ports is one host's detected ports and forwards, as pfwd ls --json shows
// them.
func (s *Server) ports(w http.ResponseWriter, r *http.Request) {
	params, _ := json.Marshal(map[string]string{"host": r.PathValue("host")})
	res, err := s.b.Handle(r.Context(), "list", params)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := json.Marshal(res)
	if err != nil {
		writeError(w, err)
		return
	}
	var list struct {
		Hosts []json.RawMessage `json:"hosts"`
	}
	if err := json.Unmarshal(data, &list); err != nil || len(list.Hosts) == 0 {
		writeError(w, ipc.Errorf(ipc.CodeNotFound, "the daemon isn't connected to %s; connect it first", r.PathValue("host")))
		return
	}
	writeJSON(w, http.StatusOK, list.Hosts[0])
}

// patchForward pins, unpins or relabels a forward.
func (s *Server) patchForward(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pinned *bool   `json:"pinned"`
		Label  *string `json:"label"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("id")
	if body.Pinned == nil && body.Label == nil {
		writeError(w, ipc.Errorf(ipc.CodeUsage, "nothing to change: send pinned or label"))
		return
	}
	if body.Pinned != nil {
		params, _ := json.Marshal(map[string]any{"id": id, "pinned": *body.Pinned})
		if _, err := s.b.Handle(r.Context(), "set_pinned", params); err != nil {
			writeError(w, err)
			return
		}
	}
	if body.Label != nil {
		params, _ := json.Marshal(map[string]any{"id": id, "label": *body.Label})
		if _, err := s.b.Handle(r.Context(), "set_label", params); err != nil {
			writeError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// statusOf maps an error code to an HTTP status.
func statusOf(code string) int {
	switch code {
	case ipc.CodeUsage:
		return http.StatusBadRequest
	case ipc.CodeNotFound:
		return http.StatusNotFound
	case ipc.CodeBind:
		return http.StatusConflict
	case ipc.CodeConnect, ipc.CodeAuth, ipc.CodeHostKey:
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

func writeError(w http.ResponseWriter, err error) {
	var e *ipc.Error
	if !errors.As(err, &e) {
		e = &ipc.Error{Code: ipc.CodeInternal, Message: err.Error()}
	}
	writeStatus(w, statusOf(e.Code), e.Code, e.Message)
}

func writeStatus(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]ipc.Error{"error": {Code: code, Message: msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
