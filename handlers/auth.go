package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"deployable/models"
)

const (
	bcryptCost        = 12
	sessionCookie     = "session_id"
	sessionMaxAgeSec  = 60 * 60 * 24 * 30 // 30 days
	otpEmailMinutes   = 15
	resetEmailMinutes = 60
)

// RegisterPage godoc
// @Summary      Registration form
// @Tags         auth
// @Produce      html
// @Success      200  {string}  string  "HTML form"
// @Router       /register [get]
func RegisterPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deps.Render(w, "register", map[string]any{"Title": "Register"})
	}
}

// Register godoc
// @Summary      Create an account
// @Description  Validates name/email/password (min 8 chars), bcrypt-hashes the password (cost 12), and creates an unverified user. Does NOT create a session — a 6-digit OTP is emailed and the user is redirected to /verify-email; login is blocked until the OTP is confirmed. Duplicate emails and validation failures re-render the form with 200 and an inline error.
// @Tags         auth
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        name      formData  string  true  "Full name"
// @Param        email     formData  string  true  "Email address"
// @Param        password  formData  string  true  "Password, minimum 8 characters"
// @Success      303  {string}  string  "Redirects to /verify-email?email=..."
// @Success      200  {string}  string  "Validation failed or email already registered — form re-rendered with error"
// @Router       /register [post]
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

		issueAndSendOTP(deps, user)

		http.Redirect(w, r, "/verify-email?email="+url.QueryEscape(user.Email), http.StatusSeeOther)
	}
}

// VerifyEmailPage godoc
// @Summary      Email verification form
// @Description  Shows the OTP entry form for the email address passed as a query param.
// @Tags         auth
// @Produce      html
// @Param        email  query  string  false  "Email address to verify"
// @Success      200  {string}  string  "HTML form"
// @Router       /verify-email [get]
func VerifyEmailPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deps.Render(w, "verify-email", map[string]any{
			"Title": "Verify Email",
			"Email": r.URL.Query().Get("email"),
		})
	}
}

// VerifyEmail godoc
// @Summary      Confirm the OTP sent at registration
// @Description  Validates the 6-digit code against the most recently issued OTP for the account (15-minute expiry, 5 attempt limit). On success marks the account verified and redirects to /login. Does not create a session — the user still needs to sign in.
// @Tags         auth
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        email  formData  string  true  "Email address being verified"
// @Param        code   formData  string  true  "6-digit code from the verification email"
// @Success      303  {string}  string  "Redirects to /login?verified=1"
// @Success      200  {string}  string  "Invalid, expired, or locked code — form re-rendered with error"
// @Router       /verify-email [post]
func VerifyEmail(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			renderVerifyError(deps, w, "", "Invalid form submission")
			return
		}

		email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
		code := strings.TrimSpace(r.FormValue("code"))

		user, err := models.FindUserByEmail(r.Context(), deps.Pool, email)
		if err != nil {
			renderVerifyError(deps, w, email, "Could not verify — please register again")
			return
		}
		if user.IsEmailVerified() {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		switch err := models.VerifyEmailOTP(r.Context(), deps.Pool, user.ID, code); {
		case err == nil:
			// verified below
		case errors.Is(err, models.ErrOTPInvalid):
			renderVerifyError(deps, w, email, "Incorrect code, please try again")
			return
		case errors.Is(err, models.ErrOTPExpired):
			renderVerifyError(deps, w, email, "Code expired — request a new one below")
			return
		case errors.Is(err, models.ErrOTPLocked):
			renderVerifyError(deps, w, email, "Too many attempts — request a new code below")
			return
		case errors.Is(err, models.ErrNotFound):
			renderVerifyError(deps, w, email, "No code found — request a new one below")
			return
		default:
			renderVerifyError(deps, w, email, "Something went wrong, please try again")
			return
		}

		if err := models.MarkEmailVerified(r.Context(), deps.Pool, user.ID); err != nil {
			renderVerifyError(deps, w, email, "Verification succeeded but activation failed — please contact support")
			return
		}

		http.Redirect(w, r, "/login?verified=1", http.StatusSeeOther)
	}
}

// ResendOTP godoc
// @Summary      Resend the verification code
// @Description  Issues and emails a new OTP, subject to a 60-second cooldown since the last one. Always responds with the same generic notice regardless of whether the account exists or is already verified, to avoid leaking account state.
// @Tags         auth
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        email  formData  string  true  "Email address to resend the code to"
// @Success      200  {string}  string  "Verify-email form re-rendered with a notice"
// @Router       /resend-otp [post]
func ResendOTP(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			renderVerifyError(deps, w, "", "Invalid form submission")
			return
		}
		email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
		notice := "If that account exists and isn't verified yet, a new code has been sent."

		user, err := models.FindUserByEmail(r.Context(), deps.Pool, email)
		if err != nil || user.IsEmailVerified() {
			renderVerifyNotice(deps, w, email, notice)
			return
		}

		canResend, err := models.CanResendEmailVerification(r.Context(), deps.Pool, user.ID)
		if err != nil {
			renderVerifyError(deps, w, email, "Something went wrong, please try again")
			return
		}
		if !canResend {
			renderVerifyNotice(deps, w, email, "Please wait a minute before requesting another code.")
			return
		}

		issueAndSendOTP(deps, user)
		renderVerifyNotice(deps, w, email, notice)
	}
}

// LoginPage godoc
// @Summary      Login form
// @Tags         auth
// @Produce      html
// @Param        verified     query  string  false  "Set to 1 after successful email verification, shows a confirmation notice"
// @Param        reset        query  string  false  "Set to 1 after a successful password reset, shows a confirmation notice"
// @Param        oauth_error  query  string  false  "Set to 1 when GitHub/Google sign-in fails or is not configured, shows an error"
// @Success      200  {string}  string  "HTML form"
// @Router       /login [get]
func LoginPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{"Title": "Sign In"}
		switch {
		case r.URL.Query().Get("verified") == "1":
			data["Notice"] = "Email verified! Please sign in."
		case r.URL.Query().Get("reset") == "1":
			data["Notice"] = "Password reset! Please sign in with your new password."
		case r.URL.Query().Get("oauth_error") == "1":
			data["Error"] = "Sign-in with that provider didn't work — please try again or use email/password."
		}
		deps.Render(w, "login", data)
	}
}

// Login godoc
// @Summary      Sign in
// @Description  Validates credentials against the stored bcrypt hash, then requires the account's email to be verified. On success creates a session, sets the session_id cookie, records last_login_at, and emails a welcome message the first time this succeeds for the account. On failure re-renders the login form with a generic error — no session is created and which field was wrong is never disclosed.
// @Tags         auth
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        email     formData  string  true  "Email address"
// @Param        password  formData  string  true  "Password"
// @Success      303  {string}  string  "Redirects to /analyze; Set-Cookie: session_id=..."
// @Success      200  {string}  string  "Invalid credentials or unverified email — form re-rendered with error, no session created"
// @Router       /login [post]
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

		if user.PasswordHash == "" {
			renderLoginError(deps, w, "This account uses GitHub or Google sign-in — use a button below", email)
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
			renderLoginError(deps, w, "Invalid email or password", email)
			return
		}

		if !user.IsEmailVerified() {
			renderLoginError(deps, w, "Please verify your email before signing in", email)
			return
		}

		expiresAt := time.Now().Add(30 * 24 * time.Hour)
		session, err := models.CreateSession(r.Context(), deps.Pool, user.ID, expiresAt)
		if err != nil {
			renderLoginError(deps, w, "Could not sign in, please try again", email)
			return
		}

		setSessionCookie(w, session.ID)
		completeLogin(deps, user)
		http.Redirect(w, r, "/analyze", http.StatusSeeOther)
	}
}

// Logout godoc
// @Summary      Sign out
// @Description  Deletes the session from Postgres and Redis, clears the session_id cookie (Max-Age=0), and redirects to /login. Requires a session_id cookie.
// @Tags         auth
// @Success      303  {string}  string  "Redirects to /login; cookie cleared"
// @Router       /logout [post]
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

// ForgotPasswordPage godoc
// @Summary      Forgot-password form
// @Tags         auth
// @Produce      html
// @Success      200  {string}  string  "HTML form"
// @Router       /forgot-password [get]
func ForgotPasswordPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deps.Render(w, "forgot-password", map[string]any{"Title": "Forgot Password"})
	}
}

// ForgotPassword godoc
// @Summary      Request a password reset link
// @Description  Always responds with the same generic notice regardless of whether the email is registered, to avoid account enumeration. If it is, a single-use reset link (1-hour expiry) is emailed.
// @Tags         auth
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        email  formData  string  true  "Email address"
// @Success      200  {string}  string  "Generic confirmation notice"
// @Router       /forgot-password [post]
func ForgotPassword(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			deps.Render(w, "forgot-password", map[string]any{"Title": "Forgot Password"})
			return
		}
		email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))

		if user, err := models.FindUserByEmail(r.Context(), deps.Pool, email); err == nil {
			if token, err := models.CreatePasswordReset(r.Context(), deps.Pool, user.ID); err == nil {
				sendResetEmail(deps, user, token)
			} else {
				log.Printf("create password reset: %v", err)
			}
		}

		deps.Render(w, "forgot-password", map[string]any{
			"Title":  "Forgot Password",
			"Notice": "If an account exists for that email, we've sent a password reset link.",
		})
	}
}

// ResetPasswordPage godoc
// @Summary      Reset-password form
// @Description  Validates the token from the emailed link before showing the new-password form.
// @Tags         auth
// @Produce      html
// @Param        token  query  string  true  "Password reset token"
// @Success      200  {string}  string  "HTML form, or an invalid/expired notice"
// @Router       /reset-password [get]
func ResetPasswordPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			deps.Render(w, "reset-password", map[string]any{"Title": "Reset Password", "Invalid": true})
			return
		}
		if _, err := models.FindValidPasswordReset(r.Context(), deps.Pool, token); err != nil {
			deps.Render(w, "reset-password", map[string]any{"Title": "Reset Password", "Invalid": true})
			return
		}
		deps.Render(w, "reset-password", map[string]any{"Title": "Reset Password", "Token": token})
	}
}

// ResetPassword godoc
// @Summary      Set a new password
// @Description  Validates the single-use token, updates the bcrypt hash (cost 12), consumes the token, and deletes every existing session for the account (forcing re-authentication everywhere) before redirecting to /login.
// @Tags         auth
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        token     formData  string  true  "Password reset token"
// @Param        password  formData  string  true  "New password, minimum 8 characters"
// @Success      303  {string}  string  "Redirects to /login?reset=1"
// @Success      200  {string}  string  "Invalid/expired token or validation error — form re-rendered"
// @Router       /reset-password [post]
func ResetPassword(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			deps.Render(w, "reset-password", map[string]any{"Title": "Reset Password", "Invalid": true})
			return
		}
		token := r.FormValue("token")
		password := r.FormValue("password")

		reset, err := models.FindValidPasswordReset(r.Context(), deps.Pool, token)
		if err != nil {
			deps.Render(w, "reset-password", map[string]any{"Title": "Reset Password", "Invalid": true})
			return
		}

		if len(password) < 8 {
			deps.Render(w, "reset-password", map[string]any{
				"Title": "Reset Password", "Token": token, "Error": "Password must be at least 8 characters",
			})
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
		if err != nil {
			deps.Render(w, "reset-password", map[string]any{
				"Title": "Reset Password", "Token": token, "Error": "Could not process password",
			})
			return
		}

		if err := models.UpdatePassword(r.Context(), deps.Pool, reset.UserID, string(hash)); err != nil {
			deps.Render(w, "reset-password", map[string]any{
				"Title": "Reset Password", "Token": token, "Error": "Could not reset password, please try again",
			})
			return
		}

		_ = models.ConsumePasswordReset(r.Context(), deps.Pool, reset.ID)
		_ = models.DeleteAllUserSessions(r.Context(), deps.Pool, reset.UserID)

		http.Redirect(w, r, "/login?reset=1", http.StatusSeeOther)
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

// completeLogin records the login timestamp and fires the welcome email the
// first time it succeeds for an account.
func completeLogin(deps Deps, user *models.User) {
	isFirstLogin, err := models.RecordLogin(context.Background(), deps.Pool, user.ID)
	if err != nil {
		log.Printf("record login: %v", err)
		return
	}
	if isFirstLogin {
		sendWelcomeEmail(deps, user)
	}
}

func issueAndSendOTP(deps Deps, user *models.User) {
	code, err := models.CreateEmailVerification(context.Background(), deps.Pool, user.ID)
	if err != nil {
		log.Printf("create email verification: %v", err)
		return
	}
	sendOTPEmail(deps, user, code)
}

func sendOTPEmail(deps Deps, user *models.User, code string) {
	body, err := deps.RenderString("email-otp", map[string]any{
		"Name":             user.Name,
		"Code":             code,
		"ExpiresInMinutes": otpEmailMinutes,
	})
	if err != nil {
		log.Printf("render otp email: %v", err)
		return
	}
	go func() {
		if err := deps.Mailer.Send(context.Background(), user.Email, "Verify your email — Deployable", body); err != nil {
			log.Printf("send otp email: %v", err)
		}
	}()
}

func sendWelcomeEmail(deps Deps, user *models.User) {
	body, err := deps.RenderString("email-welcome", map[string]any{
		"Name":   user.Name,
		"AppURL": deps.AppURL + "/analyze",
	})
	if err != nil {
		log.Printf("render welcome email: %v", err)
		return
	}
	go func() {
		if err := deps.Mailer.Send(context.Background(), user.Email, "Welcome to Deployable", body); err != nil {
			log.Printf("send welcome email: %v", err)
			return
		}
		if err := models.MarkWelcomed(context.Background(), deps.Pool, user.ID); err != nil {
			log.Printf("mark welcomed: %v", err)
		}
	}()
}

func sendResetEmail(deps Deps, user *models.User, token string) {
	resetURL := deps.AppURL + "/reset-password?token=" + url.QueryEscape(token)
	body, err := deps.RenderString("email-reset-password", map[string]any{
		"Name":             user.Name,
		"ResetURL":         resetURL,
		"ExpiresInMinutes": resetEmailMinutes,
	})
	if err != nil {
		log.Printf("render reset email: %v", err)
		return
	}
	go func() {
		if err := deps.Mailer.Send(context.Background(), user.Email, "Reset your password — Deployable", body); err != nil {
			log.Printf("send reset email: %v", err)
		}
	}()
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

func renderVerifyError(deps Deps, w http.ResponseWriter, email, msg string) {
	deps.Render(w, "verify-email", map[string]any{
		"Title": "Verify Email",
		"Email": email,
		"Error": msg,
	})
}

func renderVerifyNotice(deps Deps, w http.ResponseWriter, email, msg string) {
	deps.Render(w, "verify-email", map[string]any{
		"Title":  "Verify Email",
		"Email":  email,
		"Notice": msg,
	})
}
