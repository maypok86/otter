package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/maypok86/otter/v2"
	"github.com/maypok86/otter/v2/stats"
)

// User represents a user entity in the system
type User struct {
	ID        int64     `db:"id"`         // Unique identifier
	Email     string    `db:"email"`      // User's email address
	CreatedAt time.Time `db:"created_at"` // Timestamp when user was created
	UpdatedAt time.Time `db:"updated_at"` // Timestamp when user was last updated
}

// Repo provides direct database access to user data
type Repo struct {
	db *sqlx.DB // Database connection handle
}

// NewRepo creates a new repository instance
func NewRepo(db *sqlx.DB) *Repo {
	return &Repo{db: db}
}

// GetByID retrieves a user by ID from the database
func (r *Repo) GetByID(ctx context.Context, id int64) (User, error) {
	const query = "SELECT * FROM users WHERE id = $1" // SQL query with parameter binding

	var user User
	// Execute query and map result to User struct
	if err := r.db.GetContext(ctx, &user, query, id); err != nil {
		return User{}, fmt.Errorf("get user from db: %w", err) // Wrap error with context
	}
	return user, nil
}

// CachedRepo provides cached access to user data
type CachedRepo struct {
	cache  *otter.Cache[int64, User] // Cache instance storing User objects
	loader otter.Loader[int64, User] // Loading function for cache misses
}

// NewCachedRepo creates a new cached repository with Otter cache
func NewCachedRepo(repo *Repo) *CachedRepo {
	// Loader function that gets called on cache misses
	loader := func(ctx context.Context, key int64) (User, error) {
		user, err := repo.GetByID(ctx, key)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Convert "not found" DB error to cache-specific error
				return User{}, otter.ErrNotFound
			}
			return User{}, err
		}
		return user, nil
	}

	// Initialize and configure Otter cache with:
	cache := otter.Must(&otter.Options[int64, User]{
		MaximumSize:       10_000,                                              // Maximum cache capacity
		ExpiryCalculator:  otter.ExpiryWriting[int64, User](time.Hour),         // Entry TTL (time-to-live)
		RefreshCalculator: otter.RefreshWriting[int64, User](50 * time.Minute), // Refresh interval
		StatsRecorder:     stats.NewCounter(),                                  // Cache statistics collector
	})

	return &CachedRepo{
		cache:  cache,
		loader: otter.LoaderFunc[int64, User](loader), // Convert loader to Otter-compatible type
	}
}

// GetByID retrieves a user by ID, using cache when possible
func (cr *CachedRepo) GetByID(ctx context.Context, id int64) (User, error) {
	// Get from cache, calling loader on cache miss
	return cr.cache.Get(ctx, id, cr.loader)
}
