package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var ErrInvalidCredentials = errors.New("auth: invalid credentials")

type Repo struct{ db *gorm.DB }

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Create inserts a new user with a bcrypt-hashed password. Returns the
// new user's ID.
func (r *Repo) Create(ctx context.Context, email, displayName, password, role string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return "", errors.New("auth: email and password required")
	}
	if role == "" {
		role = RoleMember
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth bcrypt: %w", err)
	}
	u := User{
		ID:           uuid.NewString(),
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: hash,
		Role:         role,
		CreatedAt:    time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Create(&u).Error; err != nil {
		return "", fmt.Errorf("auth create: %w", err)
	}
	return u.ID, nil
}

// Authenticate looks up a user by email and verifies their bcrypt
// password. ErrInvalidCredentials masks both "user not found" and
// "wrong password" — callers should not distinguish.
func (r *Repo) Authenticate(ctx context.Context, email, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var u User
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, fmt.Errorf("auth lookup: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword(u.PasswordHash, []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

// Find returns a user by ID; sql.ErrNoRows-equivalent maps to gorm.ErrRecordNotFound.
func (r *Repo) Find(ctx context.Context, id string) (User, error) {
	var u User
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&u).Error; err != nil {
		return User{}, err
	}
	return u, nil
}
