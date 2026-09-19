package repository

import (
	"context"
	"errors"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"gorm.io/gorm"
)

type Blog struct{ db *gorm.DB }

func NewBlog(db *gorm.DB) *Blog { return &Blog{db: db} }
func (r *Blog) Create(ctx context.Context, row model.Blog) (model.Blog, error) {
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return model.Blog{}, blogError(err)
	}
	return r.ByID(ctx, row.ID)
}
func (r *Blog) query(ctx context.Context) *gorm.DB {
	// Count authoritative relationship rows instead of maintaining a second counter that could drift.
	return r.db.WithContext(ctx).Model(&model.Blog{}).Select("blog.*, users.nickname, users.avatar, (SELECT COUNT(*) FROM blog_like WHERE blog_like.blog_id = blog.id) AS like_count").Joins("JOIN users ON users.id = blog.user_id")
}
func (r *Blog) ByID(ctx context.Context, id uint64) (model.Blog, error) {
	var row model.Blog
	if err := r.query(ctx).First(&row, id).Error; err != nil {
		return model.Blog{}, blogError(err)
	}
	return row, nil
}
func (r *Blog) List(ctx context.Context, shopID, userID uint64, offset, limit int) ([]model.Blog, error) {
	query := r.query(ctx)
	if shopID != 0 {
		query = query.Where("blog.shop_id = ?", shopID)
	}
	if userID != 0 {
		query = query.Where("blog.user_id = ?", userID)
	}
	var rows []model.Blog
	if err := query.Order("blog.created_at DESC").Order("blog.id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, blogError(err)
	}
	return rows, nil
}
func (r *Blog) SetLike(ctx context.Context, blogID, userID uint64, liked bool) (bool, error) {
	return r.SetLikeChanged(ctx, blogID, userID, liked)
}
func (r *Blog) IsLiked(ctx context.Context, blogID, userID uint64) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.BlogLike{}).Where("blog_id = ? AND user_id = ?", blogID, userID).Count(&count).Error; err != nil {
		return false, blogError(err)
	}
	return count > 0, nil
}
func blogError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, gorm.ErrForeignKeyViolated) {
		return apperror.New(apperror.NotFound, err)
	}
	return apperror.New(apperror.Dependency, err)
}
