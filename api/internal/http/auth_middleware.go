package httpx

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type authContextKey string

type authInfo struct {
	UserID string
	TeamID string
}

const contextKeyAuth authContextKey = "peep-auth-info"

type contextSetter interface {
	SetContext(context.Context)
}

// requireAuth ensures the request has a valid bearer token before invoking the handler.
func (r *Router) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx, _, ok := r.ensureAuth(w, req)
		if !ok {
			return
		}
		if setter, ok := w.(contextSetter); ok {
			setter.SetContext(ctx)
		}
		next(w, req.WithContext(ctx))
	}
}

// ensureAuth validates the Authorization header and enriches the context.
func (r *Router) ensureAuth(w http.ResponseWriter, req *http.Request) (context.Context, authInfo, bool) {
	token, err := bearerToken(req.Header.Get("Authorization"))
	if err != nil {
		r.logger.Warn("authorization header invalid", "error", err, "path", req.URL.Path)
		writeError(w, http.StatusUnauthorized, "authentication required")
		return req.Context(), authInfo{}, false
	}
	user, claims, err := r.auth.Authorize(req.Context(), token)
	if err != nil {
		r.logger.Warn("token validation failed", "error", err, "path", req.URL.Path)
		writeError(w, http.StatusUnauthorized, "authentication failed")
		return req.Context(), authInfo{}, false
	}
	info := authInfo{UserID: user.ID, TeamID: claims.TeamID}
	ctx := context.WithValue(req.Context(), contextKeyAuth, info)
	return ctx, info, true
}

// authInfoFromContext extracts auth metadata from context.
func authInfoFromContext(ctx context.Context) (authInfo, bool) {
	value := ctx.Value(contextKeyAuth)
	if value == nil {
		return authInfo{}, false
	}
	info, ok := value.(authInfo)
	return info, ok
}

func bearerToken(header string) (string, error) {
	if strings.TrimSpace(header) == "" {
		return "", errors.New("missing authorization header")
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("invalid authorization header format")
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", errors.New("empty bearer token")
	}
	return token, nil
}
