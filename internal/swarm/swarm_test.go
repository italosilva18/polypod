package swarm

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// testLogger returns a slog.Logger that discards output — tests should not
// pollute stderr.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- SwarmManager construction & basic CRUD ---

func TestNewSwarmManager(t *testing.T) {
	m := NewSwarmManager(testLogger())
	if m == nil {
		t.Fatal("NewSwarmManager returned nil")
	}
	if got := m.ListTeams(); len(got) != 0 {
		t.Errorf("new manager should have 0 teams, got %d", len(got))
	}
}

func TestSwarmManager_CreateTeam(t *testing.T) {
	m := NewSwarmManager(testLogger())
	cfg := DefaultTeamConfig()

	team, err := m.CreateTeam("alpha", "first team", cfg)
	if err != nil {
		t.Fatalf("CreateTeam error: %v", err)
	}
	if team == nil {
		t.Fatal("CreateTeam returned nil team")
	}
	if team.Name != "alpha" {
		t.Errorf("team name = %q, want %q", team.Name, "alpha")
	}
	if team.Description != "first team" {
		t.Errorf("team description = %q", team.Description)
	}
	if team.State != TeamStateCreated {
		t.Errorf("new team state = %v, want TeamStateCreated", team.State)
	}
	if team.LeaderName != TeamLeaderName {
		t.Errorf("leader name = %q, want %q", team.LeaderName, TeamLeaderName)
	}
	if len(team.Members) != 1 {
		t.Errorf("expected 1 initial member (the leader), got %d", len(team.Members))
	}
	if team.LeaderID == "" {
		t.Error("leader ID should not be empty")
	}
	if team.Router() == nil {
		t.Error("team router should not be nil")
	}
	if team.PermissionBridgeRef() == nil {
		t.Error("team permission bridge should not be nil")
	}
	if team.Context() == nil {
		t.Error("team context should not be nil")
	}
}

func TestSwarmManager_CreateTeam_Duplicate(t *testing.T) {
	m := NewSwarmManager(testLogger())
	cfg := DefaultTeamConfig()

	if _, err := m.CreateTeam("dup", "", cfg); err != nil {
		t.Fatalf("first CreateTeam: %v", err)
	}
	_, err := m.CreateTeam("dup", "", cfg)
	if err == nil {
		t.Fatal("expected error on duplicate team name")
	}
	if !errors.Is(err, ErrTeamExists) {
		t.Errorf("error = %v, want ErrTeamExists", err)
	}
}

func TestSwarmManager_GetTeam(t *testing.T) {
	m := NewSwarmManager(testLogger())
	if _, ok := m.GetTeam("ghost"); ok {
		t.Error("GetTeam for missing team should report ok=false")
	}

	created, err := m.CreateTeam("real", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	got, ok := m.GetTeam("real")
	if !ok {
		t.Fatal("GetTeam should find created team")
	}
	if got != created {
		t.Error("GetTeam returned a different *Team pointer than CreateTeam")
	}
}

func TestSwarmManager_ListTeams(t *testing.T) {
	m := NewSwarmManager(testLogger())
	names := []string{"one", "two", "three"}
	for _, n := range names {
		if _, err := m.CreateTeam(n, "", DefaultTeamConfig()); err != nil {
			t.Fatalf("CreateTeam(%q): %v", n, err)
		}
	}
	list := m.ListTeams()
	if len(list) != len(names) {
		t.Fatalf("ListTeams returned %d, want %d", len(list), len(names))
	}
	found := make(map[string]bool)
	for _, s := range list {
		found[s.Name] = true
		if s.State != TeamStateCreated.String() {
			t.Errorf("team %q state = %q, want %q", s.Name, s.State, TeamStateCreated.String())
		}
		if s.MemberCount != 1 {
			t.Errorf("team %q MemberCount = %d, want 1", s.Name, s.MemberCount)
		}
		if s.LeaderID == "" {
			t.Errorf("team %q LeaderID empty", s.Name)
		}
	}
	for _, n := range names {
		if !found[n] {
			t.Errorf("team %q missing from ListTeams", n)
		}
	}
}

func TestSwarmManager_DeleteTeam(t *testing.T) {
	m := NewSwarmManager(testLogger())
	if _, err := m.CreateTeam("doomed", "", DefaultTeamConfig()); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := m.DeleteTeam("doomed", 2*time.Second); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if _, ok := m.GetTeam("doomed"); ok {
		t.Error("deleted team should not be retrievable")
	}
	if got := m.ListTeams(); len(got) != 0 {
		t.Errorf("expected 0 teams after delete, got %d", len(got))
	}

	// Deleting a non-existent team must return ErrTeamNotFound.
	err := m.DeleteTeam("doomed", time.Second)
	if !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("DeleteTeam missing = %v, want ErrTeamNotFound", err)
	}
}

func TestSwarmManager_Shutdown(t *testing.T) {
	m := NewSwarmManager(testLogger())
	for _, n := range []string{"a", "b", "c"} {
		if _, err := m.CreateTeam(n, "", DefaultTeamConfig()); err != nil {
			t.Fatalf("CreateTeam(%q): %v", n, err)
		}
	}
	// Shutdown should drain all teams without panicking.
	m.Shutdown(2 * time.Second)
	if got := m.ListTeams(); len(got) != 0 {
		t.Errorf("after Shutdown expected 0 teams, got %d", len(got))
	}
}

// --- Team state transitions ---

func TestTeam_StateTransitions(t *testing.T) {
	m := NewSwarmManager(testLogger())
	team, err := m.CreateTeam("states", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	// Created → Running
	if err := team.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if team.State != TeamStateRunning {
		t.Fatalf("after Start state = %v, want Running", team.State)
	}

	// Running → Paused
	if err := team.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if team.State != TeamStatePaused {
		t.Fatalf("after Pause state = %v, want Paused", team.State)
	}

	// Pausing again is illegal (state != Running).
	if err := team.Pause(); err == nil {
		t.Error("second Pause should error")
	}

	// Paused → Running via Resume.
	if err := team.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if team.State != TeamStateRunning {
		t.Fatalf("after Resume state = %v, want Running", team.State)
	}

	// Running → Stopped via Stop.
	if err := team.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if team.State != TeamStateStopped {
		t.Fatalf("after Stop state = %v, want Stopped", team.State)
	}

	// Starting a stopped team must fail.
	if err := team.Start(); err == nil {
		t.Error("Start on stopped team should error")
	}

	// Stop is idempotent.
	if err := team.Stop(time.Second); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestTeamState_String(t *testing.T) {
	cases := map[TeamState]string{
		TeamStateCreated:  "created",
		TeamStateRunning:  "running",
		TeamStatePaused:   "paused",
		TeamStateStopping: "stopping",
		TeamStateStopped:  "stopped",
		TeamState(99):     "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("TeamState(%d).String() = %q, want %q", s, got, want)
		}
	}
}

// --- Agent assignment / teammate management ---

func TestTeam_AddTeammate(t *testing.T) {
	m := NewSwarmManager(testLogger())
	team, err := m.CreateTeam("workers", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := team.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	info, err := team.AddTeammate(TeammateConfig{
		Name:      "alice",
		AgentType: "coder",
		Model:     "claude",
	})
	if err != nil {
		t.Fatalf("AddTeammate: %v", err)
	}
	if info == nil {
		t.Fatal("AddTeammate returned nil MemberInfo")
	}
	if info.Name != "alice" {
		t.Errorf("name = %q, want alice", info.Name)
	}
	if info.AgentID == "" {
		t.Error("AgentID should not be empty")
	}
	if !info.IsIdle {
		t.Error("new teammate should be marked idle")
	}

	// The team should now have 2 members (leader + alice).
	members := team.ListMembers()
	if len(members) != 2 {
		t.Fatalf("member count = %d, want 2", len(members))
	}

	// Lookup by name.
	m2, ok := team.GetMember("alice")
	if !ok {
		t.Fatal("GetMember(alice) not found")
	}
	if m2.AgentID != info.AgentID {
		t.Errorf("GetMember returned different AgentID: %q vs %q", m2.AgentID, info.AgentID)
	}

	// Leader is also retrievable.
	if _, ok := team.GetMember(TeamLeaderName); !ok {
		t.Error("leader should be retrievable via GetMember")
	}

	// Missing member.
	if _, ok := team.GetMember("nobody"); ok {
		t.Error("GetMember(nobody) should return ok=false")
	}

	// Router should have a mailbox for alice.
	if mb := team.Router().GetMailbox("alice"); mb == nil {
		t.Error("router should have a mailbox for alice after AddTeammate")
	}
}

func TestTeam_AddTeammate_UniqueName(t *testing.T) {
	m := NewSwarmManager(testLogger())
	team, err := m.CreateTeam("unique", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := team.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	first, err := team.AddTeammate(TeammateConfig{Name: "bob"})
	if err != nil {
		t.Fatalf("first AddTeammate: %v", err)
	}
	if first.Name != "bob" {
		t.Errorf("first name = %q, want bob", first.Name)
	}

	// Second teammate with the same base name should get a numeric suffix.
	second, err := team.AddTeammate(TeammateConfig{Name: "bob"})
	if err != nil {
		t.Fatalf("second AddTeammate: %v", err)
	}
	if second.Name == "bob" {
		t.Error("second teammate should have a different name from the first")
	}
	if !strings.HasPrefix(second.Name, "bob") {
		t.Errorf("second teammate name = %q, expected bob-prefixed", second.Name)
	}
}

func TestTeam_AddTeammate_NotRunning(t *testing.T) {
	m := NewSwarmManager(testLogger())
	team, err := m.CreateTeam("stopped", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	// Stop the team without starting.
	if err := team.Stop(time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	_, err = team.AddTeammate(TeammateConfig{Name: "late"})
	if !errors.Is(err, ErrTeamNotRunning) {
		t.Errorf("AddTeammate on stopped team error = %v, want ErrTeamNotRunning", err)
	}
}

func TestTeam_AddTeammate_MaxMembers(t *testing.T) {
	m := NewSwarmManager(testLogger())
	cfg := DefaultTeamConfig()
	cfg.MaxMembers = 2
	team, err := m.CreateTeam("capped", "", cfg)
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := team.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for i, name := range []string{"one", "two"} {
		if _, err := team.AddTeammate(TeammateConfig{Name: name}); err != nil {
			t.Fatalf("AddTeammate #%d: %v", i, err)
		}
	}
	// Third should be rejected with ErrTeamFull.
	_, err = team.AddTeammate(TeammateConfig{Name: "three"})
	if !errors.Is(err, ErrTeamFull) {
		t.Errorf("over-capacity error = %v, want ErrTeamFull", err)
	}
}

func TestTeam_RemoveTeammate(t *testing.T) {
	m := NewSwarmManager(testLogger())
	team, err := m.CreateTeam("remove", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := team.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := team.AddTeammate(TeammateConfig{Name: "charlie"}); err != nil {
		t.Fatalf("AddTeammate: %v", err)
	}

	if err := team.RemoveTeammate("charlie"); err != nil {
		t.Fatalf("RemoveTeammate: %v", err)
	}
	if _, ok := team.GetMember("charlie"); ok {
		t.Error("charlie should be gone")
	}
	if len(team.ListMembers()) != 1 {
		t.Errorf("expected 1 member (leader) after remove, got %d", len(team.ListMembers()))
	}

	// Removing again returns ErrAgentNotFound.
	err = team.RemoveTeammate("charlie")
	if !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("RemoveTeammate missing = %v, want ErrAgentNotFound", err)
	}

	// The leader cannot be removed (matches the same error path because
	// the filter skips TeamLeaderName).
	if err := team.RemoveTeammate(TeamLeaderName); !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("RemoveTeammate(leader) = %v, want ErrAgentNotFound", err)
	}
}

func TestTeam_ListIdleAndRunning(t *testing.T) {
	m := NewSwarmManager(testLogger())
	team, err := m.CreateTeam("idle", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := team.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := team.Stop(2 * time.Second); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	if _, err := team.AddTeammate(TeammateConfig{Name: "w1"}); err != nil {
		t.Fatalf("AddTeammate: %v", err)
	}
	if _, err := team.AddTeammate(TeammateConfig{Name: "w2"}); err != nil {
		t.Fatalf("AddTeammate: %v", err)
	}

	idle := team.IdleTeammates()
	if len(idle) != 0 {
		t.Errorf("unstarted teammates appear idle: %v", idle)
	}
	if running := team.RunningTeammates(); len(running) != 0 {
		t.Errorf("running teammates = %d, want 0 (no runners started)", len(running))
	}

	// Metadata alone cannot make an unstarted teammate available for work.
	team.SetMemberIdle("w1", false)
	if member, ok := team.GetMember("w1"); !ok || member.IsIdle {
		t.Errorf("SetMemberIdle did not update member metadata: %+v", member)
	}
	if idle := team.IdleTeammates(); len(idle) != 0 {
		t.Errorf("unstarted teammates appear idle after metadata update: %v", idle)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	if err := team.StartTeammate("w1", func(ctx context.Context, _ string, _ TeammateIdentity) (string, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "done", nil
	}); err != nil {
		t.Fatalf("StartTeammate(w1): %v", err)
	}
	if err := team.StartTeammate("w2", func(context.Context, string, TeammateIdentity) (string, error) {
		return "done", nil
	}); err != nil {
		t.Fatalf("StartTeammate(w2): %v", err)
	}
	if idle := team.IdleTeammates(); len(idle) != 2 {
		t.Fatalf("started idle teammates = %v, want w1 and w2", idle)
	}

	if err := team.Router().SendMessage(NewMessage(MessageTaskAssignment, TeamLeaderName, "w1", "work")); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("w1 did not start work")
	}
	if running := team.RunningTeammates(); len(running) != 1 || running[0] != "w1" {
		t.Errorf("running teammates = %v, want w1", running)
	}
	if idle := team.IdleTeammates(); len(idle) != 1 || idle[0] != "w2" {
		t.Errorf("idle teammates while w1 works = %v, want w2", idle)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for len(team.IdleTeammates()) != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if idle := team.IdleTeammates(); len(idle) != 2 {
		t.Errorf("teammates after work completed = %v, want w1 and w2", idle)
	}
}

func TestTeam_Summary(t *testing.T) {
	m := NewSwarmManager(testLogger())
	cfg := DefaultTeamConfig()
	team, err := m.CreateTeam("sum", "description goes here", cfg)
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	s := team.Summary()
	if s.Name != "sum" {
		t.Errorf("Name = %q", s.Name)
	}
	if s.Description != "description goes here" {
		t.Errorf("Description = %q", s.Description)
	}
	if s.MemberCount != 1 {
		t.Errorf("MemberCount = %d, want 1", s.MemberCount)
	}
	if s.State != "created" {
		t.Errorf("State = %q, want created", s.State)
	}
	if s.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

// --- Helper functions ---

func TestDefaultTeamConfig(t *testing.T) {
	c := DefaultTeamConfig()
	if c.MaxMembers != 20 {
		t.Errorf("MaxMembers = %d, want 20", c.MaxMembers)
	}
	if c.DefaultTimeout != 5*time.Minute {
		t.Errorf("DefaultTimeout = %v, want 5m", c.DefaultTimeout)
	}
}

func TestFormatAgentIDAndSanitizeName(t *testing.T) {
	cases := []struct {
		name, team, want string
	}{
		{"alice", "squad", "alice@squad"},
		{"Alice", "Squad", "alice@squad"},
		{"alice bob", "my team", "alice-bob@my-team"},
		{"weird!name", "t.eam", "weird-name@t-eam"},
	}
	for _, c := range cases {
		if got := FormatAgentID(c.name, c.team); got != c.want {
			t.Errorf("FormatAgentID(%q,%q) = %q, want %q", c.name, c.team, got, c.want)
		}
	}

	if got := SanitizeName("Hello World!"); got != "hello-world-" {
		t.Errorf("SanitizeName = %q, want %q", got, "hello-world-")
	}
	if got := SanitizeName("abc123"); got != "abc123" {
		t.Errorf("SanitizeName(alnum) = %q, want abc123", got)
	}
}

// --- Task distribution via CoordinatorMode ---

func TestCoordinatorMode_DelegateTask(t *testing.T) {
	m := NewSwarmManager(testLogger())
	team, err := m.CreateTeam("coord", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := team.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := team.AddTeammate(TeammateConfig{Name: "worker1"}); err != nil {
		t.Fatalf("AddTeammate: %v", err)
	}

	cm := NewCoordinatorMode(CoordinatorConfig{
		Team:   team,
		Logger: testLogger(),
	})
	if cm == nil {
		t.Fatal("NewCoordinatorMode returned nil")
	}

	// Delegating to an unknown worker must return ErrAgentNotFound.
	if _, err := cm.DelegateTask("ghost", "task", "do something"); !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("DelegateTask(ghost) error = %v, want ErrAgentNotFound", err)
	}

	// Delegating to a registered worker should succeed and return a non-empty task ID.
	taskID, err := cm.DelegateTask("worker1", "research", "look into X")
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if taskID == "" {
		t.Error("DelegateTask returned empty taskID")
	}

	// The worker's mailbox should now contain a task-assignment message.
	mb := team.Router().GetMailbox("worker1")
	if mb == nil {
		t.Fatal("worker1 mailbox missing")
	}
	if mb.Pending() == 0 {
		t.Error("worker1 should have a pending task assignment message")
	}
}

// --- Messaging: ensures router plumbing is exercised via the public API ---

func TestTeam_RouterBroadcastAndSend(t *testing.T) {
	m := NewSwarmManager(testLogger())
	team, err := m.CreateTeam("msg", "", DefaultTeamConfig())
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := team.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := team.AddTeammate(TeammateConfig{Name: "recv"}); err != nil {
		t.Fatalf("AddTeammate: %v", err)
	}

	router := team.Router()
	// Directed send.
	msg := NewMessage(MessageChat, TeamLeaderName, "recv", "hello")
	if err := router.SendMessage(msg); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// Send to unknown agent must fail with ErrAgentNotFound.
	bad := NewMessage(MessageChat, TeamLeaderName, "nope", "x")
	if err := router.SendMessage(bad); !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("SendMessage unknown target = %v, want ErrAgentNotFound", err)
	}

	// Broadcast from leader should reach recv (but not leader itself).
	bmsg := NewMessage(MessageBroadcast, TeamLeaderName, "", "announce")
	delivered := router.BroadcastMessage(bmsg)
	if len(delivered) != 1 || delivered[0] != "recv" {
		t.Errorf("broadcast delivered = %v, want [recv]", delivered)
	}

	// ListAgents should include leader + recv.
	agents := router.ListAgents()
	if len(agents) != 2 {
		t.Errorf("ListAgents = %v, want 2 entries", agents)
	}
}
