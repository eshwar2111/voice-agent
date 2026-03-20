package memory

import "time"

// MemoryType categorizes what kind of memory this is.
type MemoryType string

const (
	TypeEpisodic MemoryType = "episodic" // What happened (events, actions)
	TypeSemantic MemoryType = "semantic" // Facts and knowledge
	TypeTask     MemoryType = "task"     // Completed tasks with outcomes
)

// Importance tiers for filtering and TTL logic.
const (
	ImportanceLow    = 1
	ImportanceMedium = 5
	ImportanceHigh   = 10
)

// Memory is a single unit of agent memory.
type Memory struct {
	ID         string
	Type       MemoryType
	Content    string
	Importance int
	Timestamp  time.Time
}
