package swarm

import "errors"

var (
	// ErrTeamExists is returned when attempting to create a team that already exists.
	ErrTeamExists = errors.New("team already exists")
	// ErrTeamNotFound is returned when referencing a non-existent team.
	ErrTeamNotFound = errors.New("team not found")
	// ErrAgentNotFound is returned when routing to an unregistered agent.
	ErrAgentNotFound = errors.New("agent not found")
	// ErrMailboxClosed is returned when delivering to a closed mailbox.
	ErrMailboxClosed = errors.New("mailbox closed")
	// ErrTeamFull is returned when the team has reached its max member count.
	ErrTeamFull = errors.New("team is at maximum capacity")
	// ErrTeamNotRunning is returned for operations on a non-running team.
	ErrTeamNotRunning = errors.New("team is not running")
	// ErrTeammateDuplicate is returned when a teammate name conflicts.
	ErrTeammateDuplicate = errors.New("teammate name already exists in team")
	// ErrPermissionTimeout is returned when a permission request times out.
	ErrPermissionTimeout = errors.New("permission request timed out")
	// ErrShutdownInProgress is returned when shutdown is already happening.
	ErrShutdownInProgress = errors.New("shutdown already in progress")
)
