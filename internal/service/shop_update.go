package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type ShopEditor interface {
	UpdateText(context.Context, uint64, model.ShopTextEdit) (model.Shop, error)
}
type ShopInvalidator interface {
	Invalidate(context.Context, uint64) error
}
type ShopMaintenance struct {
	shops ShopEditor
	store ShopInvalidator
}

func NewShopMaintenance(shops ShopEditor, store ShopInvalidator) *ShopMaintenance {
	return &ShopMaintenance{shops: shops, store: store}
}
func (s *ShopMaintenance) Update(ctx context.Context, id uint64, edit model.ShopTextEdit) (ShopView, error) {
	if id == 0 || (edit.Name == nil && edit.Address == nil) {
		return ShopView{}, apperror.New(apperror.Validation, nil)
	}
	if edit.Name != nil {
		name := strings.TrimSpace(*edit.Name)
		if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 128 {
			return ShopView{}, apperror.New(apperror.Validation, nil)
		}
		edit.Name = &name
	}
	if edit.Address != nil {
		address := strings.TrimSpace(*edit.Address)
		if !utf8.ValidString(address) || utf8.RuneCountInString(address) > 512 {
			return ShopView{}, apperror.New(apperror.Validation, nil)
		}
		edit.Address = &address
	}
	row, err := s.shops.UpdateText(ctx, id, edit)
	if err != nil {
		return ShopView{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := s.store.Invalidate(bounded, id); err != nil {
		return shopView(row), apperror.New(apperror.Dependency, err)
	}
	return shopView(row), nil
}
