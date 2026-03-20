package entity

import "sort"

// RoomID is the unique identifier for a chat room.
type RoomID string

// RoomType distinguishes 1:1 (direct) rooms from multi-member (group) rooms.
type RoomType string

const (
	// RoomTypeDirect is a 1:1 private conversation between exactly two users.
	RoomTypeDirect RoomType = "direct"
	// RoomTypeGroup is a room with two or more members.
	RoomTypeGroup RoomType = "group"
)

// Room is an aggregate representing a chat room and its membership.
type Room struct {
	ID      RoomID             `json:"id"`
	Type    RoomType           `json:"type"`
	Name    string             `json:"name,omitempty"`
	Members map[UserID]struct{} `json:"-"`
}

// DirectRoomID returns a deterministic, canonical room ID for a 1:1 conversation
// between two users. The ID is stable regardless of argument order.
//
// Example: DirectRoomID("bob", "alice") == DirectRoomID("alice", "bob")
func DirectRoomID(a, b UserID) RoomID {
	ids := []string{string(a), string(b)}
	sort.Strings(ids)
	return RoomID("direct:" + ids[0] + ":" + ids[1])
}
