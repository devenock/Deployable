package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	tokencrypto "deployable/internal/crypto"
	ghclient "deployable/internal/github"
	"deployable/middleware"
	"deployable/models"
)

// repoButtonData renders the Add/Remove toggle button for one repo — used
// both for the picker's initial row list and as the response to the
// add/remove actions themselves (hx-target="this" swaps just the button).
type repoButtonData struct {
	GitHubID      int64
	FullName      string
	Private       bool
	DefaultBranch string
	ConnectedID   string // non-empty (a connected_repos UUID) when already added
}

// ListGitHubRepos godoc
// @Summary      Browse your GitHub repositories
// @Description  Requires a session cookie and a connected GitHub account (see GET /auth/github/connect). Lists the caller's repos from the GitHub API, most recently updated first, marking which are already on the watchlist. Returns a "connect GitHub" prompt instead if no token is on file.
// @Tags         web
// @Produce      html
// @Param        page  query  int  false  "Page number, 1-indexed"
// @Success      200  {string}  string  "HTML partial"
// @Router       /account/github/repos [get]
func ListGitHubRepos(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context())

		client, ok := githubClientForUser(deps, r, user.ID)
		if !ok {
			deps.Render(w, "repo-picker-results", map[string]any{"NotConnected": true})
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}

		repos, hasMore, err := client.ListUserRepos(r.Context(), page)
		if err != nil {
			if errors.Is(err, ghclient.ErrUnauthenticated) {
				deps.Render(w, "repo-picker-results", map[string]any{"NotConnected": true})
				return
			}
			log.Printf("list github repos for user %s: %v", user.ID, err)
			http.Error(w, "could not load your GitHub repositories, please try again", http.StatusBadGateway)
			return
		}

		connected, err := models.ListConnectedRepos(r.Context(), deps.Pool, user.ID)
		if err != nil {
			log.Printf("list connected repos for user %s: %v", user.ID, err)
			http.Error(w, "could not load your repositories", http.StatusInternalServerError)
			return
		}
		connectedByGitHubID := make(map[int64]uuid.UUID, len(connected))
		for _, c := range connected {
			connectedByGitHubID[c.GitHubID] = c.ID
		}

		rows := make([]repoButtonData, len(repos))
		for i, repo := range repos {
			rows[i] = repoButtonData{
				GitHubID:      repo.ID,
				FullName:      repo.FullName,
				Private:       repo.Private,
				DefaultBranch: repo.DefaultBranch,
			}
			if id, ok := connectedByGitHubID[repo.ID]; ok {
				rows[i].ConnectedID = id.String()
			}
		}

		data := map[string]any{"Rows": rows, "Page": page, "HasMore": hasMore}
		if page == 1 {
			deps.Render(w, "repo-picker-results", data)
		} else {
			deps.Render(w, "repo-picker-page", data)
		}
	}
}

// AddGitHubRepo godoc
// @Summary      Add a repository to your watchlist
// @Description  Requires a session cookie. Adds (or refreshes, if already added) a repo on the caller's connected-repos watchlist — this only tracks it, it doesn't scan it. Trusts the client-supplied repo metadata (matching the picker it came from) rather than re-verifying against GitHub, since the watchlist is a personal list scoped to the caller and scanning always re-validates against the real repo anyway.
// @Tags         web
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        github_id       formData  int     true   "GitHub's numeric repository ID"
// @Param        full_name       formData  string  true   "owner/repo"
// @Param        private         formData  bool    false  "Whether the repo is private"
// @Param        default_branch  formData  string  false  "Default branch name"
// @Success      200  {string}  string  "HTML partial: the toggle button, now in Remove state"
// @Router       /account/github/repos [post]
func AddGitHubRepo(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context())
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		githubID, err := strconv.ParseInt(r.FormValue("github_id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid repository", http.StatusBadRequest)
			return
		}
		fullName := r.FormValue("full_name")
		private := r.FormValue("private") == "true"
		defaultBranch := r.FormValue("default_branch")

		repo, err := models.AddConnectedRepo(r.Context(), deps.Pool, user.ID, githubID, fullName, private, defaultBranch)
		if err != nil {
			log.Printf("add connected repo for user %s: %v", user.ID, err)
			http.Error(w, "could not add repository", http.StatusInternalServerError)
			return
		}

		deps.Render(w, "repo-action-button", repoButtonData{
			GitHubID:      repo.GitHubID,
			FullName:      repo.FullName,
			Private:       repo.Private,
			DefaultBranch: repo.DefaultBranch,
			ConnectedID:   repo.ID.String(),
		})
	}
}

// RemoveGitHubRepo godoc
// @Summary      Remove a repository from your watchlist
// @Description  Requires a session cookie and ownership. Removes the watchlist entry only — any reports already generated from this repo are untouched. With ?render=button (used by the repo picker), re-renders the same toggle button in "Add" state using the repo metadata passed as query params, instead of the default empty response (used by the dashboard's connected-repos list, where the row is simply removed).
// @Tags         web
// @Produce      html
// @Param        id              path   string  true   "connected_repos row ID"
// @Param        render          query  string  false  "Set to 'button' to get the re-rendered Add button instead of an empty response"
// @Param        github_id       query  int     false  "GitHub's numeric repository ID (only used with render=button)"
// @Param        full_name       query  string  false  "owner/repo (only used with render=button)"
// @Param        private         query  bool    false  "Whether the repo is private (only used with render=button)"
// @Param        default_branch  query  string  false  "Default branch name (only used with render=button)"
// @Success      200  {string}  string  "Empty, or the toggle button in Add state (see render param)"
// @Failure      404  {string}  string  "Unknown watchlist entry, or not the owner"
// @Router       /account/github/repos/{id} [delete]
func RemoveGitHubRepo(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context())

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if err := models.RemoveConnectedRepo(r.Context(), deps.Pool, id, user.ID); err != nil {
			http.NotFound(w, r)
			return
		}

		// The picker's row asks for render=button (?render=button, plus the
		// repo's metadata as query params) so it can swap itself back into
		// "Add" state via hx-target="this" — it's still showing a real,
		// browsable GitHub repo, just no longer on the watchlist. The
		// dashboard's connected-repos list has no use for that: the row
		// there only exists because the repo IS connected, so it just wants
		// the row gone (hx-target on the row, empty response removes it).
		if r.URL.Query().Get("render") == "button" {
			githubID, _ := strconv.ParseInt(r.URL.Query().Get("github_id"), 10, 64)
			deps.Render(w, "repo-action-button", repoButtonData{
				GitHubID:      githubID,
				FullName:      r.URL.Query().Get("full_name"),
				Private:       r.URL.Query().Get("private") == "true",
				DefaultBranch: r.URL.Query().Get("default_branch"),
			})
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

// githubClientForUser builds a GitHub API client authenticated with the
// user's stored token. ok is false if they haven't connected GitHub (or the
// stored token failed to decrypt) — callers should show a connect prompt.
func githubClientForUser(deps Deps, r *http.Request, userID uuid.UUID) (*ghclient.Client, bool) {
	encrypted, err := models.GetGitHubToken(r.Context(), deps.Pool, userID)
	if err != nil {
		return nil, false
	}
	token, err := tokencrypto.DecryptToken(encrypted)
	if err != nil {
		log.Printf("decrypt github token for user %s: %v", userID, err)
		return nil, false
	}
	return ghclient.NewClient(token), true
}
