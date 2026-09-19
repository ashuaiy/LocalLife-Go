package repository

import (
	"context"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

func (r *Shop) ByIDs(ctx context.Context, ids []uint64) ([]model.Shop, error) {
	rows := make([]model.Shop, 0, len(ids))
	if len(ids) == 0 {
		return rows, nil
	}
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, apperror.New(apperror.Dependency, err)
	}
	return rows, nil
}
func (r *Shop) ForType(ctx context.Context, typeID uint64) ([]model.Shop, error) {
	var rows []model.Shop
	if err := r.db.WithContext(ctx).Where("type_id = ?", typeID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, apperror.New(apperror.Dependency, err)
	}
	return rows, nil
}
