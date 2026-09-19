package service

import (
	"context"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type Follows interface {
	Set(ctx context.Context, userID, targetID uint64, following bool) error
	State(ctx context.Context, userID, targetID uint64) (bool, error)
	Following(ctx context.Context, userID uint64, offset, limit int) ([]model.User, error)
}
type FollowUsers interface {
	Exists(context.Context, uint64) (bool, error)
}
type Follow struct {
	follows Follows
	users   FollowUsers
}

func NewFollow(follows Follows, users FollowUsers) *Follow {
	return &Follow{follows: follows, users: users}
}

type FollowResult struct {
	Following bool `json:"following"`
}
type FollowingPage struct {
	Items    []PublicUser `json:"items"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	HasMore  bool         `json:"has_more"`
}

func (f *Follow) validate(ctx context.Context, userID, targetID uint64) error {
	if userID == 0 {
		return apperror.New(apperror.Unauthorized, nil)
	}
	if targetID == 0 || targetID == userID {
		return apperror.New(apperror.Validation, nil)
	}
	exists, err := f.users.Exists(ctx, targetID)
	if err != nil {
		return err
	}
	if !exists {
		return apperror.New(apperror.NotFound, nil)
	}
	return nil
}
func (f *Follow) Set(ctx context.Context, userID, targetID uint64, following bool) (FollowResult, error) {
	if err := f.validate(ctx, userID, targetID); err != nil {
		return FollowResult{}, err
	}
	if err := f.follows.Set(ctx, userID, targetID, following); err != nil {
		return FollowResult{}, err
	}
	return FollowResult{Following: following}, nil
}
func (f *Follow) State(ctx context.Context, userID, targetID uint64) (FollowResult, error) {
	if err := f.validate(ctx, userID, targetID); err != nil {
		return FollowResult{}, err
	}
	state, err := f.follows.State(ctx, userID, targetID)
	if err != nil {
		return FollowResult{}, err
	}
	return FollowResult{Following: state}, nil
}
func (f *Follow) List(ctx context.Context, userID uint64, page, size int) (FollowingPage, error) {
	if userID == 0 {
		return FollowingPage{}, apperror.New(apperror.Unauthorized, nil)
	}
	if page < 1 || page > 1000 || size < 1 || size > 50 {
		return FollowingPage{}, apperror.New(apperror.Validation, nil)
	}
	rows, err := f.follows.Following(ctx, userID, (page-1)*size, size+1)
	if err != nil {
		return FollowingPage{}, err
	}
	result := FollowingPage{Items: make([]PublicUser, 0, min(len(rows), size)), Page: page, PageSize: size, HasMore: len(rows) > size}
	if result.HasMore {
		rows = rows[:size]
	}
	for _, row := range rows {
		result.Items = append(result.Items, publicUser(row))
	}
	return result, nil
}
