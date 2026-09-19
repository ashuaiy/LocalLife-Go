package model

// Profile contains only public fields; authentication credentials never enter this projection.
type Profile struct {
	ID        uint64 `json:"id,string"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	City      string `json:"city"`
	Introduce string `json:"introduce"`
	Gender    uint8  `json:"gender"`
	Birthday  string `json:"birthday"`
	Credits   uint32 `json:"credits"`
	Level     uint32 `json:"level"`
	Fans      int64  `json:"fans"`
	Following int64  `json:"following"`
}

type LikeMessage struct {
	ID       string `json:"id"`
	BlogID   uint64 `json:"blog_id,string"`
	UserID   uint64 `json:"user_id,string"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}
