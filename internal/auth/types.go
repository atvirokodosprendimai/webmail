// Package auth manages webmail users: bcrypt-hashed local accounts that
// log into the shared mailbox. There is no self-signup — admins seed
// users via `webmail user add`.
package auth

import "time"

const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// User is a webmail account. Each user has their own login but shares
// the single configured IMAP/SMTP mailbox with every other user.
type User struct {
	ID           string    `gorm:"primaryKey;column:id"`
	Email        string    `gorm:"uniqueIndex;not null;column:email"`
	DisplayName  string    `gorm:"column:display_name"`
	PasswordHash []byte    `gorm:"column:password_hash"`
	Role         string    `gorm:"column:role"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (User) TableName() string { return "users" }
