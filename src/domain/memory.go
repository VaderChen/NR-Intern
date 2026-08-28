package domain

import "time"

type MemoryKind string

const (
	MemoryKindFact       MemoryKind = "fact"
	MemoryKindPreference MemoryKind = "preference"
	MemoryKindDecision   MemoryKind = "decision"
	MemoryKindProcedure  MemoryKind = "procedure"
	MemoryKindConstraint MemoryKind = "constraint"
)

type MemoryStatus string

const (
	MemoryStatusActive     MemoryStatus = "active"
	MemoryStatusSuperseded MemoryStatus = "superseded"
	MemoryStatusForgotten  MemoryStatus = "forgotten"
)

type Memory struct {
	ID              string         `json:"id"`
	Scope           string         `json:"scope"`
	Kind            MemoryKind     `json:"kind"`
	Content         string         `json:"content"`
	Tags            []string       `json:"tags,omitempty"`
	Confidence      float64        `json:"confidence"`
	Status          MemoryStatus   `json:"status"`
	SourceSessionID string         `json:"source_session_id,omitempty"`
	SourceMessageID string         `json:"source_message_id,omitempty"`
	Supersedes      []string       `json:"supersedes,omitempty"`
	SupersededBy    string         `json:"superseded_by,omitempty"`
	ForgetReason    string         `json:"forget_reason,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	LastAccessedAt  *time.Time     `json:"last_accessed_at,omitempty"`
	ForgottenAt     *time.Time     `json:"forgotten_at,omitempty"`
}

type RememberMemoryInput struct {
	Scope           string         `json:"scope"`
	Kind            MemoryKind     `json:"kind"`
	Content         string         `json:"content"`
	Tags            []string       `json:"tags,omitempty"`
	Confidence      float64        `json:"confidence,omitempty"`
	SourceSessionID string         `json:"source_session_id,omitempty"`
	SourceMessageID string         `json:"source_message_id,omitempty"`
	Supersedes      []string       `json:"supersedes,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type MemoryQuery struct {
	Scope string       `json:"scope"`
	Text  string       `json:"text,omitempty"`
	Kinds []MemoryKind `json:"kinds,omitempty"`
	Tags  []string     `json:"tags,omitempty"`
	Limit int          `json:"limit,omitempty"`
}
