package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	tokencrypto "deployable/internal/crypto"
	"deployable/middleware"
	"deployable/models"
)

const (
	oauthStateCookie    = "oauth_state"
	oauthIntentCookie   = "oauth_intent"
	oauthReturnToCookie = "oauth_return_to"
	oauthStateTTL       = 10 * time.Minute
)

// oauthProfile is the subset of a provider's user profile we need,
// normalized across GitHub and Google.
type oauthProfile struct {
	ID        string // provider-scoped account ID
	Login     string // GitHub username; empty for Google
	Email     string // primary, provider-verified email (empty if unavailable)
	Name      string
	AvatarURL string // GitHub only
}

// GitHubOAuthStart godoc
// @Summary      Start GitHub sign-in
// @Description  Redirects to GitHub's OAuth authorize page. Sets a short-lived oauth_state cookie for CSRF protection, checked on callback.
// @Tags         auth
// @Success      303  {string}  string  "Redirects to github.com/login/oauth/authorize"
// @Router       /auth/github [get]
func GitHubOAuthStart(deps Deps) http.HandlerFunc {
	return oauthStart(deps.GitHubOAuth)
}

// GitHubOAuthCallback godoc
// @Summary      GitHub OAuth callback
// @Description  Exchanges the code for a token, fetches the GitHub profile (id, login, primary verified email), links-or-creates the account by GitHub ID — falling back to matching an existing account by email — trusts the provider-verified email, creates a session, and redirects to /analyze.
// @Tags         auth
// @Param        code   query  string  true  "Authorization code from GitHub"
// @Param        state  query  string  true  "CSRF state, must match the oauth_state cookie"
// @Success      303  {string}  string  "Redirects to /analyze; Set-Cookie: session_id=..."
// @Failure      303  {string}  string  "Redirects to /login?oauth_error=1 on failure"
// @Router       /auth/github/callback [get]
func GitHubOAuthCallback(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		intent, returnTo := readAndClearOAuthIntent(w, r)

		if !validOAuthState(w, r) {
			http.Redirect(w, r, oauthErrorRedirect(intent, returnTo), http.StatusSeeOther)
			return
		}

		token, err := deps.GitHubOAuth.Exchange(r.Context(), r.URL.Query().Get("code"))
		if err != nil {
			log.Printf("github oauth exchange: %v", err)
			http.Redirect(w, r, oauthErrorRedirect(intent, returnTo), http.StatusSeeOther)
			return
		}

		if intent == "connect" {
			completeGitHubConnect(w, r, deps, token, returnTo)
			return
		}

		profile, err := fetchGitHubProfile(deps.GitHubOAuth.Client(r.Context(), token))
		if err != nil {
			log.Printf("fetch github profile: %v", err)
			http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
			return
		}

		completeOAuthLogin(w, r, deps, "github", profile)
	}
}

// GitHubConnectStart godoc
// @Summary      Connect a GitHub account for private repository access
// @Description  Requires an active session (see RequireAuth). Redirects to GitHub's OAuth authorize page requesting `repo` scope — broader than the read:user/user:email scope used for sign-in — so the resulting token can download private repository archives. GitHubOAuthCallback encrypts it (AES-256-GCM, ENCRYPTION_KEY) and adds it as a connected account (a user can connect more than one — reconnecting the same GitHub account refreshes its token rather than adding a duplicate).
// @Tags         analyze
// @Param        return_to  query  string  false  "Local path to redirect back to after connecting (default /analyze)"
// @Success      303  {string}  string  "Redirects to github.com/login/oauth/authorize"
// @Router       /auth/github/connect [get]
func GitHubConnectStart(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.GitHubOAuth == nil || deps.GitHubOAuth.ClientID == "" {
			http.Redirect(w, r, "/analyze?oauth_error=1", http.StatusSeeOther)
			return
		}

		state, err := generateOAuthState()
		if err != nil {
			http.Redirect(w, r, "/analyze?oauth_error=1", http.StatusSeeOther)
			return
		}

		http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: state, Path: "/", MaxAge: int(oauthStateTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{Name: oauthIntentCookie, Value: "connect", Path: "/", MaxAge: int(oauthStateTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{Name: oauthReturnToCookie, Value: sanitizeReturnTo(r.URL.Query().Get("return_to")), Path: "/", MaxAge: int(oauthStateTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode})

		repoScopeConfig := *deps.GitHubOAuth
		repoScopeConfig.Scopes = []string{"repo"}
		http.Redirect(w, r, repoScopeConfig.AuthCodeURL(state), http.StatusSeeOther)
	}
}

// completeGitHubConnect stores the repo-scoped token (encrypted) on the
// currently logged-in user and redirects back to returnTo. Requires
// GitHubOAuthCallback's route to be wrapped in middleware.OptionalAuth so a
// user is present in context.
func completeGitHubConnect(w http.ResponseWriter, r *http.Request, deps Deps, token *oauth2.Token, returnTo string) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
		return
	}

	profile, err := fetchGitHubProfile(deps.GitHubOAuth.Client(r.Context(), token))
	if err != nil {
		log.Printf("fetch github profile for connect: %v", err)
		http.Redirect(w, r, returnTo+"?oauth_error=1", http.StatusSeeOther)
		return
	}

	encrypted, err := tokencrypto.EncryptToken(token.AccessToken)
	if err != nil {
		log.Printf("encrypt github token: %v", err)
		http.Redirect(w, r, returnTo+"?oauth_error=1", http.StatusSeeOther)
		return
	}

	if _, err := models.AddGitHubAccount(r.Context(), deps.Pool, user.ID, profile.ID, profile.Login, profile.AvatarURL, encrypted); err != nil {
		log.Printf("add github account: %v", err)
		http.Redirect(w, r, returnTo+"?oauth_error=1", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, returnTo+"?github_connected=1", http.StatusSeeOther)
}

// readAndClearOAuthIntent reads (and clears) the intent/return_to cookies
// set by GitHubConnectStart. Absent cookies mean the ordinary login flow.
func readAndClearOAuthIntent(w http.ResponseWriter, r *http.Request) (intent, returnTo string) {
	if c, err := r.Cookie(oauthIntentCookie); err == nil {
		intent = c.Value
	}
	returnTo = sanitizeReturnTo("")
	if c, err := r.Cookie(oauthReturnToCookie); err == nil && c.Value != "" {
		returnTo = c.Value
	}
	http.SetCookie(w, &http.Cookie{Name: oauthIntentCookie, Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: oauthReturnToCookie, Value: "", Path: "/", MaxAge: -1})
	return intent, returnTo
}

func oauthErrorRedirect(intent, returnTo string) string {
	if intent == "connect" {
		return returnTo + "?oauth_error=1"
	}
	return "/login?oauth_error=1"
}

// sanitizeReturnTo only allows a local, single-segment-root path, to avoid
// using an attacker-supplied return_to as an open redirect.
func sanitizeReturnTo(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "/analyze"
	}
	return path
}

// GoogleOAuthStart godoc
// @Summary      Start Google sign-in
// @Description  Redirects to Google's OAuth consent page. Sets a short-lived oauth_state cookie for CSRF protection, checked on callback.
// @Tags         auth
// @Success      303  {string}  string  "Redirects to accounts.google.com/o/oauth2/auth"
// @Router       /auth/google [get]
func GoogleOAuthStart(deps Deps) http.HandlerFunc {
	return oauthStart(deps.GoogleOAuth)
}

// GoogleOAuthCallback godoc
// @Summary      Google OAuth callback
// @Description  Exchanges the code for a token, fetches the Google profile (sub, verified email, name), links-or-creates the account by Google ID — falling back to matching an existing account by email — trusts the provider-verified email, creates a session, and redirects to /analyze.
// @Tags         auth
// @Param        code   query  string  true  "Authorization code from Google"
// @Param        state  query  string  true  "CSRF state, must match the oauth_state cookie"
// @Success      303  {string}  string  "Redirects to /analyze; Set-Cookie: session_id=..."
// @Failure      303  {string}  string  "Redirects to /login?oauth_error=1 on failure"
// @Router       /auth/google/callback [get]
func GoogleOAuthCallback(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validOAuthState(w, r) {
			http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
			return
		}

		token, err := deps.GoogleOAuth.Exchange(r.Context(), r.URL.Query().Get("code"))
		if err != nil {
			log.Printf("google oauth exchange: %v", err)
			http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
			return
		}

		profile, err := fetchGoogleProfile(deps.GoogleOAuth.Client(r.Context(), token))
		if err != nil {
			log.Printf("fetch google profile: %v", err)
			http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
			return
		}

		completeOAuthLogin(w, r, deps, "google", profile)
	}
}

func oauthStart(config *oauth2.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if config == nil || config.ClientID == "" {
			http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
			return
		}

		state, err := generateOAuthState()
		if err != nil {
			http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     oauthStateCookie,
			Value:    state,
			Path:     "/",
			MaxAge:   int(oauthStateTTL.Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		http.Redirect(w, r, config.AuthCodeURL(state), http.StatusSeeOther)
	}
}

func validOAuthState(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie(oauthStateCookie)
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/", MaxAge: -1})
	if err != nil || cookie.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(r.URL.Query().Get("state"))) == 1
}

func generateOAuthState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// completeOAuthLogin finds the account linked to this provider ID, or links
// it to an existing account matched by email, or creates a new account —
// then signs the resulting user in exactly like a password login.
func completeOAuthLogin(w http.ResponseWriter, r *http.Request, deps Deps, provider string, profile oauthProfile) {
	ctx := r.Context()

	user, err := findUserByProvider(ctx, deps.Pool, provider, profile.ID)
	switch {
	case err == nil:
		// existing linked account
	case errors.Is(err, models.ErrNotFound):
		user, err = linkOrCreateOAuthUser(ctx, deps.Pool, provider, profile)
		if err != nil {
			log.Printf("oauth link/create user (%s): %v", provider, err)
			http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
			return
		}
	default:
		log.Printf("oauth lookup user (%s): %v", provider, err)
		http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
		return
	}

	expiresAt := time.Now().Add(30 * 24 * time.Hour)
	session, err := models.CreateSession(ctx, deps.Pool, user.ID, expiresAt)
	if err != nil {
		log.Printf("create session after oauth login: %v", err)
		http.Redirect(w, r, "/login?oauth_error=1", http.StatusSeeOther)
		return
	}

	setSessionCookie(w, session.ID)
	completeLogin(deps, user)
	http.Redirect(w, r, "/analyze", http.StatusSeeOther)
}

func findUserByProvider(ctx context.Context, pool *pgxpool.Pool, provider, providerID string) (*models.User, error) {
	if provider == "github" {
		return models.FindUserByGitHubID(ctx, pool, providerID)
	}
	return models.FindUserByGoogleID(ctx, pool, providerID)
}

func linkOrCreateOAuthUser(ctx context.Context, pool *pgxpool.Pool, provider string, profile oauthProfile) (*models.User, error) {
	if profile.Email == "" {
		return nil, fmt.Errorf("%s did not return a verified email", provider)
	}

	if existing, err := models.FindUserByEmail(ctx, pool, profile.Email); err == nil {
		if provider == "github" {
			if err := models.LinkGitHubAccount(ctx, pool, existing.ID, profile.ID, profile.Login); err != nil {
				return nil, err
			}
		} else {
			if err := models.LinkGoogleAccount(ctx, pool, existing.ID, profile.ID); err != nil {
				return nil, err
			}
		}
		if !existing.IsEmailVerified() {
			_ = models.MarkEmailVerified(ctx, pool, existing.ID)
		}
		return existing, nil
	}

	if provider == "github" {
		return models.CreateGitHubUser(ctx, pool, profile.Email, profile.Name, profile.ID, profile.Login)
	}
	return models.CreateGoogleUser(ctx, pool, profile.Email, profile.Name, profile.ID)
}

type githubUserResponse struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

type githubEmailResponse struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func fetchGitHubProfile(client *http.Client) (oauthProfile, error) {
	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		return oauthProfile{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return oauthProfile{}, fmt.Errorf("github /user returned %d", resp.StatusCode)
	}
	var u githubUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return oauthProfile{}, err
	}

	email := u.Email
	if email == "" {
		if emailsResp, err := client.Get("https://api.github.com/user/emails"); err == nil {
			defer emailsResp.Body.Close()
			var emails []githubEmailResponse
			if json.NewDecoder(emailsResp.Body).Decode(&emails) == nil {
				for _, e := range emails {
					if e.Primary && e.Verified {
						email = e.Email
						break
					}
				}
			}
		}
	}

	name := u.Name
	if name == "" {
		name = u.Login
	}

	return oauthProfile{
		ID:        strconv.FormatInt(u.ID, 10),
		Login:     u.Login,
		Email:     email,
		Name:      name,
		AvatarURL: u.AvatarURL,
	}, nil
}

type googleUserInfoResponse struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

func fetchGoogleProfile(client *http.Client) (oauthProfile, error) {
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return oauthProfile{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return oauthProfile{}, fmt.Errorf("google userinfo returned %d", resp.StatusCode)
	}
	var u googleUserInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return oauthProfile{}, err
	}

	email := ""
	if u.EmailVerified {
		email = u.Email
	}

	return oauthProfile{
		ID:    u.Sub,
		Email: email,
		Name:  u.Name,
	}, nil
}
