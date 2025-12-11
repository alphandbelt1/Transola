package auth

import "context"

type contextKey string

const userKey contextKey = "transola-user"

// User represents the authenticated principal.
type User struct {
	ID    string
	Email string
	Role  string
}

// WithUser annotates the context.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFromContext retrieves the principal if set.
func UserFromContext(ctx context.Context) (User, bool) {
	val := ctx.Value(userKey)
	if val == nil {
		return User{}, false
	}
	u, ok := val.(User)
	return u, ok
}
