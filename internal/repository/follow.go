package repository

import (
	"context"
	"errors"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Follow struct{ db *gorm.DB }

func NewFollow(db *gorm.DB) *Follow { return &Follow{db: db} }
func (r *User) Exists(ctx context.Context, id uint64) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return false, followError(err)
	}
	return count > 0, nil
}
func (r *Follow) Set(ctx context.Context, userID, targetID uint64, following bool) error {
	if following {
		row := model.Follow{UserID: userID, FollowUserID: targetID}
		return followError(r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error)
	}
	return followError(r.db.WithContext(ctx).Where("user_id = ? AND follow_user_id = ?", userID, targetID).Delete(&model.Follow{}).Error)
}
func (r *Follow) State(ctx context.Context, userID, targetID uint64) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.Follow{}).Where("user_id = ? AND follow_user_id = ?", userID, targetID).Count(&count).Error; err != nil {
		return false, followError(err)
	}
	return count > 0, nil
}
func (r *Follow) Following(ctx context.Context, userID uint64, offset, limit int) ([]model.User, error) {
	var rows []model.User
	err := r.db.WithContext(ctx).Model(&model.User{}).Select("users.id, users.nickname, users.avatar").Joins("JOIN follow f ON f.follow_user_id = users.id").Where("f.user_id = ?", userID).Order("f.id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, followError(err)
}
func (r *Follow) Followers(ctx context.Context, authorID uint64, asOf time.Time) ([]uint64, error) {
	var ids []uint64
	err := r.db.WithContext(ctx).Model(&model.Follow{}).Where("follow_user_id = ? AND created_at <= ?", authorID, asOf).Order("user_id ASC").Pluck("user_id", &ids).Error
	return ids, followError(err)
}
func (r *Blog) VisibleByIDs(ctx context.Context, userID uint64, ids []uint64) ([]model.Blog, error) {
	rows := make([]model.Blog, 0, len(ids))
	if len(ids) == 0 {
		return rows, nil
	}
	err := r.query(ctx).Joins("JOIN follow f ON f.follow_user_id = blog.user_id AND f.user_id = ? AND f.created_at <= blog.created_at", userID).Where("blog.id IN ?", ids).Find(&rows).Error
	return rows, followError(err)
}
func (r *Blog) FeedSource(ctx context.Context, userID uint64) ([]model.Blog, error) {
	var rows []model.Blog
	err := r.db.WithContext(ctx).Model(&model.Blog{}).Select("blog.id, blog.user_id, blog.created_at").Joins("JOIN follow f ON f.follow_user_id = blog.user_id AND f.user_id = ? AND f.created_at <= blog.created_at", userID).Order("blog.id ASC").Find(&rows).Error
	return rows, followError(err)
}
func followError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return apperror.New(apperror.NotFound, err)
	}
	return apperror.New(apperror.Dependency, err)
}
