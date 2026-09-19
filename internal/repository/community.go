package repository

import (
	"context"
	"github.com/ashuaiy/local-life-go/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Community struct{ db *gorm.DB }

func NewCommunity(db *gorm.DB) *Community { return &Community{db: db} }

func (r *Community) Profile(ctx context.Context, id uint64) (model.Profile, error) {
	var row model.Profile
	err := r.db.WithContext(ctx).Table("users").Select(`id,nickname,avatar,city,introduce,gender,
 COALESCE(DATE_FORMAT(birthday,'%Y-%m-%d'),'') AS birthday,credits,level,
 (SELECT COUNT(*) FROM follow WHERE follow_user_id=users.id) AS fans,
 (SELECT COUNT(*) FROM follow WHERE user_id=users.id) AS following`).Where("id=?", id).Take(&row).Error
	return row, blogError(err)
}
func (r *Community) Common(ctx context.Context, userID, targetID uint64, offset, limit int) ([]model.User, error) {
	var rows []model.User
	err := r.db.WithContext(ctx).Table("users").Select("users.id,users.nickname,users.avatar").
		Joins("JOIN follow a ON a.follow_user_id=users.id AND a.user_id=?", userID).
		Joins("JOIN follow b ON b.follow_user_id=users.id AND b.user_id=?", targetID).
		Order("users.id ASC").Offset(offset).Limit(limit).Scan(&rows).Error
	return rows, blogError(err)
}
func (r *Community) Hot(ctx context.Context, offset, limit int) ([]model.Blog, error) {
	var rows []model.Blog
	err := NewBlog(r.db).query(ctx).Order("like_count DESC").Order("blog.id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, blogError(err)
}
func (r *Community) Likers(ctx context.Context, id uint64) ([]model.User, error) {
	if _, err := NewBlog(r.db).ByID(ctx, id); err != nil {
		return nil, err
	}
	var rows []model.User
	err := r.db.WithContext(ctx).Table("users").Select("users.id,users.nickname,users.avatar").
		Joins("JOIN blog_like l ON l.user_id=users.id").Where("l.blog_id=?", id).
		Order("l.created_at ASC").Order("l.user_id ASC").Limit(5).Scan(&rows).Error
	return rows, blogError(err)
}
func (r *Community) DeleteBlog(ctx context.Context, id, userID uint64) error {
	// Serialize with likes, and remove relationships in the same transaction.
	return blogError(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.Blog
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, userID).Take(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("blog_id=?", id).Delete(&model.BlogLike{}).Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	}))
}

// SetLikeChanged preserves the transition result so repeated PUTs produce no extra notification.
func (r *Blog) SetLikeChanged(ctx context.Context, blogID, userID uint64, liked bool) (bool, error) {
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.Blog
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, blogID).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.BlogLike{}).Where("blog_id=? AND user_id=?", blogID, userID).Count(&count).Error; err != nil {
			return err
		}
		if (count > 0) == liked {
			return nil
		}
		changed = true
		if liked {
			return tx.Create(&model.BlogLike{BlogID: blogID, UserID: userID}).Error
		}
		return tx.Where("blog_id=? AND user_id=?", blogID, userID).Delete(&model.BlogLike{}).Error
	})
	return changed, blogError(err)
}
