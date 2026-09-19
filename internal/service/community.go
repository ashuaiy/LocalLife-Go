package service

import (
	"context"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"regexp"
	"strconv"
	"strings"
)

type CommunityRepository interface {
	Profile(context.Context, uint64) (model.Profile, error)
	Common(context.Context, uint64, uint64, int, int) ([]model.User, error)
	Hot(context.Context, int, int) ([]model.Blog, error)
	Likers(context.Context, uint64) ([]model.User, error)
	DeleteBlog(context.Context, uint64, uint64) error
}
type CommunityStore interface {
	Sign(context.Context, uint64, bool) (SignState, error)
	Messages(context.Context, uint64, string) ([]model.LikeMessage, error)
}
type SignState struct {
	Date   string `json:"date"`
	Signed bool   `json:"signed"`
	Streak int    `json:"streak"`
}
type Community struct {
	repo  CommunityRepository
	store CommunityStore
}

func NewCommunity(repo CommunityRepository, store CommunityStore) *Community {
	return &Community{repo: repo, store: store}
}
func (s *Community) Profile(ctx context.Context, id uint64) (model.Profile, error) {
	if id == 0 {
		return model.Profile{}, apperror.New(apperror.Validation, nil)
	}
	return s.repo.Profile(ctx, id)
}
func (s *Community) Common(ctx context.Context, userID, targetID uint64, page, size int) (FollowingPage, error) {
	if userID == 0 {
		return FollowingPage{}, apperror.New(apperror.Unauthorized, nil)
	}
	if targetID == 0 || !validPage(page, size) {
		return FollowingPage{}, apperror.New(apperror.Validation, nil)
	}
	if _, err := s.repo.Profile(ctx, targetID); err != nil {
		return FollowingPage{}, err
	}
	rows, err := s.repo.Common(ctx, userID, targetID, (page-1)*size, size+1)
	if err != nil {
		return FollowingPage{}, err
	}
	result := FollowingPage{Items: []PublicUser{}, Page: page, PageSize: size, HasMore: len(rows) > size}
	if result.HasMore {
		rows = rows[:size]
	}
	for _, row := range rows {
		result.Items = append(result.Items, publicUser(row))
	}
	return result, nil
}
func validPage(page, size int) bool { return page >= 1 && page <= 1000 && size >= 1 && size <= 50 }
func (s *Community) Hot(ctx context.Context, page, size int) (BlogPage, error) {
	if !validPage(page, size) {
		return BlogPage{}, apperror.New(apperror.Validation, nil)
	}
	rows, err := s.repo.Hot(ctx, (page-1)*size, size+1)
	if err != nil {
		return BlogPage{}, err
	}
	result := BlogPage{Items: []BlogView{}, Page: page, PageSize: size, HasMore: len(rows) > size}
	if result.HasMore {
		rows = rows[:size]
	}
	for _, row := range rows {
		result.Items = append(result.Items, blogView(row))
	}
	return result, nil
}
func (s *Community) Likers(ctx context.Context, id uint64) ([]PublicUser, error) {
	if id == 0 {
		return nil, apperror.New(apperror.Validation, nil)
	}
	rows, err := s.repo.Likers(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]PublicUser, 0, len(rows))
	for _, row := range rows {
		result = append(result, publicUser(row))
	}
	return result, nil
}
func (s *Community) DeleteBlog(ctx context.Context, id, userID uint64) error {
	if userID == 0 {
		return apperror.New(apperror.Unauthorized, nil)
	}
	if id == 0 {
		return apperror.New(apperror.Validation, nil)
	}
	return s.repo.DeleteBlog(ctx, id, userID)
}
func (s *Community) Sign(ctx context.Context, id uint64, write bool) (SignState, error) {
	if id == 0 {
		return SignState{}, apperror.New(apperror.Unauthorized, nil)
	}
	return s.store.Sign(ctx, id, write)
}

var messageCursor = regexp.MustCompile(`^[0-9]{1,20}-[0-9]{1,20}$`)

func (s *Community) Messages(ctx context.Context, id uint64, cursor string) ([]model.LikeMessage, error) {
	if id == 0 {
		return nil, apperror.New(apperror.Unauthorized, nil)
	}
	if cursor == "" {
		cursor = "0-0"
	}
	if !messageCursor.MatchString(cursor) {
		return nil, apperror.New(apperror.Validation, nil)
	}
	for _, part := range strings.Split(cursor, "-") {
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return nil, apperror.New(apperror.Validation, err)
		}
	}
	return s.store.Messages(ctx, id, cursor)
}
