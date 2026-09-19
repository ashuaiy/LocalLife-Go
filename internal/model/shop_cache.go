package model

import "time"

// ShopCacheEntry has a bounded stale window; a nil Value is an explicit negative entry.
type ShopCacheEntry struct {
	ID           uint64    `json:"id"`
	Value        *Shop     `json:"value"`
	RefreshAfter time.Time `json:"refresh_after"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Token is opaque to the service and is used for compare-and-set publication.
type ShopSnapshot struct {
	Entry *ShopCacheEntry
	Token string
}
