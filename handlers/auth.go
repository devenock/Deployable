package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"deployable/models"
)

const (
	bcryptCost       = 12
	sessionCookie    = "session_id"
	sessionMaxAgeSec = 60 * 60 * 24 * 30 // 30 days
)

// RegisterPage renders the registration form.
func RegisterPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deps.Render(w, "register", map[string]any{"Title": "Register"})
	}
}

// Register validates the submitted name/email/password, creates the user
// and an initial session, sets the session cookie, and redirects to /analyze.
func Register(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			renderRegisterError(deps, w, "Invalid form submission", "", "")
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
		password := r.FormValue("password")

		if email == "" || name == "" {
			renderRegisterError(deps, w, "Name and email are required", name, email)
			return
		}
		if len(password) < 8 {
			renderRegisterError(deps, w, "Password must be at least 8 characters", name, email)
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
		if err != nil {
			renderRegisterError(deps, w, "Could not process password", name, email)
			return
		}

		user, err := models.CreateUser(r.Context(), deps.Pool, email, name, string(hash))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				renderRegisterError(deps, w, "An account with that email already exists", name, email)
				return
			}
			renderRegisterError(deps, w, "Could not create account, please try again", name, email)
			return
		}

		expiresAt := time.Now().Add(30 * 24 * time.Hour)
		session, err := models.CreateSession(r.Context(), deps.Pool, user.ID, expiresAt)
		if err != nil {
			renderRegisterError(deps, w, "Account created but sign-in failed, please log in", "", email)
			return
		}

		setSessionCookie(w, session.ID)
		http.Redirect(w, r, "/analyze", http.StatusSeeOther)
	}
}

// LoginPage renders the login form.
func LoginPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deps.Render(w, "login", map[string]any{"Title": "Sign In"})
	}
}

// Login validates credentials, creates a session, sets the session cookie,
// and redirects to /analyze. Wrong credentials re-render the login form
// with an error and never create a session.
func Login(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			renderLoginError(deps, w, "Invalid form submission", "")
			return
		}

		email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
		password := r.FormValue("password")

		user, err := models.FindUserByEmail(r.Context(), deps.Pool, email)
		if err != nil {
			renderLoginError(deps, w, "Invalid email or password", email)
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
			renderLoginError(deps, w, "Invalid email or password", email)
			return
		}

		expiresAt := time.Now().Add(30 * 24 * time.Hour)
		session, err := models.CreateSession(r.Context(), deps.Pool, user.ID, expiresAt)
		if err != nil {
			renderLoginError(deps, w, "Could not sign in, please try again", email)
			return
		}

		setSessionCookie(w, session.ID)
		http.Redirect(w, r, "/analyze", http.StatusSeeOther)
	}
}

// Logout deletes the session, clears the cookie, and redirects to /login.
func Logout(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
			_ = models.DeleteSession(r.Context(), deps.Pool, cookie.Value)
			_ = deps.Redis.Del(r.Context(), "session:"+cookie.Value)
		}

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

func setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   sessionMaxAgeSec,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func renderLoginError(deps Deps, w http.ResponseWriter, msg, email string) {
	deps.Render(w, "login", map[string]any{
		"Title": "Sign In",
		"Error": msg,
		"Email": email,
	})
}

func renderRegisterError(deps Deps, w http.ResponseWriter, msg, name, email string) {
	deps.Render(w, "register", map[string]any{
		"Title": "Register",
		"Error": msg,
		"Name":  name,
		"Email": email,
	})
}
