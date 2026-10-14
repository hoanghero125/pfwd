package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

// loginTTL is how long a login URL works, once.
const loginTTL = 10 * time.Minute

// cookieName is the session cookie.
const cookieName = "pfwd_session"

// session is one logged-in browser tab.
type session struct {
	// token must come with every API call; the page gets it once.
	token   string
	claimed bool
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make a login token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LoginURL returns a new one-time login URL, valid for 10 minutes.
func (s *Server) LoginURL() (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := s.o.Now()
	s.mu.Lock()
	for k, exp := range s.logins {
		if now.After(exp) {
			delete(s.logins, k)
		}
	}
	s.logins[sha256.Sum256([]byte(token))] = now.Add(loginTTL)
	s.mu.Unlock()
	return s.Addr() + "/login?token=" + token, nil
}

// login swaps a login token for a session cookie, once, and sends the
// browser to the app without the token in its address bar.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	key := sha256.Sum256([]byte(r.URL.Query().Get("token")))
	now := s.o.Now()
	s.mu.Lock()
	exp, ok := s.logins[key]
	delete(s.logins, key)
	s.mu.Unlock()
	if !ok || now.After(exp) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintln(w, "This login link has expired or was used already. Run `pfwd ui` for a new one.")
		return
	}
	id, err := randomToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	token, err := randomToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	s.sessions[sha256.Sum256([]byte(id))] = &session{token: token}
	s.mu.Unlock()
	// Not Secure: the server is plain HTTP on 127.0.0.1, where browsers
	// don't all keep Secure cookies.
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}) //nolint:gosec // see above
	s.o.Logger.Info("web UI login")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// session returns the request's session, from its cookie.
func (s *Server) session(r *http.Request) (*session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sha256.Sum256([]byte(c.Value))]
	return sess, ok
}

// claim hands the session's token to the page, the first time only.
func (s *Server) claim(sess *session) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.claimed {
		return "", false
	}
	sess.claimed = true
	return sess.token, true
}

// tokenOK reports whether the request carries the session's token.
func tokenOK(sess *session, got string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(sess.token)) == 1
}
