package auth

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Session is the persisted scs session blob. Survives webmail process
// restarts (in-memory was losing every cookie on restart).
type Session struct {
	Token  string    `gorm:"primaryKey;column:token"`
	Data   []byte    `gorm:"column:data"`
	Expiry time.Time `gorm:"column:expiry;index"`
}

func (Session) TableName() string { return "sessions" }

// Store implements scs.Store backed by gorm/sqlite.
type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) Find(token string) ([]byte, bool, error) {
	var row Session
	err := s.db.WithContext(context.Background()).
		Where("token = ? AND expiry > ?", token, time.Now().UTC()).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return row.Data, true, nil
}

func (s *Store) Commit(token string, b []byte, expiry time.Time) error {
	return s.db.WithContext(context.Background()).Save(&Session{
		Token:  token,
		Data:   b,
		Expiry: expiry.UTC(),
	}).Error
}

func (s *Store) Delete(token string) error {
	return s.db.WithContext(context.Background()).
		Where("token = ?", token).
		Delete(&Session{}).Error
}
