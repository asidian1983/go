package entity

// UserID is the unique identifier for a user.
type UserID string

// User represents an authenticated participant in the chat system.
type User struct {
	ID       UserID `json:"id"`
	Username string `json:"username"`
}
