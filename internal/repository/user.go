package repository

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type User struct{ db *gorm.DB }

func NewUser(db *gorm.DB) *User { return &User{db: db} }

func (r *User) FindOrCreate(ctx context.Context, phone string) (model.User, error) {
	user := model.User{Phone: phone, Nickname: "用户_" + rand.Text()[:8]}
	// The unique phone index arbitrates concurrent first logins. Never overwrite an existing profile.
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&user).Error; err != nil {
		return model.User{}, err
	}
	var persisted model.User
	if err := r.db.WithContext(ctx).Where("phone = ?", phone).First(&persisted).Error; err != nil {
		return model.User{}, err
	}
	return persisted, nil
}

func (r *User) ByID(ctx context.Context, id uint64) (model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).First(&user, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.User{}, apperror.New(apperror.Unauthorized, nil)
	}
	if err != nil {
		return model.User{}, apperror.New(apperror.Dependency, err)
	}
	return user, nil
}
