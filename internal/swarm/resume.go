package swarm

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/tasks"
)

// TeamResumeReport is a read-only checkpoint diagnosis. Continued stays false
// until durable mailboxes, team configuration and run ownership are proven.
type TeamResumeReport struct {
	Team      string             `json:"team"`
	RunID     string             `json:"run_id,omitempty"`
	Continued bool               `json:"continued"`
	Note      string             `json:"note"`
	Members   []TeamResumeMember `json:"members,omitempty"`
}

// TeamResumeMember identifies the exact worker boundary and its blockers.
type TeamResumeMember struct {
	Agent             string            `json:"agent"`
	Turn              int               `json:"turn,omitempty"`
	Action            string            `json:"action"`
	Phase             string            `json:"phase,omitempty"`
	PendingToolCallID string            `json:"pending_tool_call_id,omitempty"`
	Blockers          []string          `json:"blockers,omitempty"`
	Issues            []TeamResumeIssue `json:"issues,omitempty"`
	Reason            string            `json:"reason"`
}

// TeamResumeIssue exposes uncertainty in an earlier checkpoint as well as the
// latest one, so a newer turn cannot conceal an unresolved tool effect.
type TeamResumeIssue struct {
	Turn              int    `json:"turn"`
	Kind              string `json:"kind"`
	PendingToolCallID string `json:"pending_tool_call_id,omitempty"`
}

// workerTurnResume remains the internal runner boundary type. No public tool
// constructs it while crash recovery is disabled.
type workerTurnResume struct {
	Turn       int
	Prompt     string
	AgentType  string
	Identity   TeammateIdentity
	Checkpoint *tasks.AgentCheckpoint
}

const resumeDiagnosticNote = "Diagnóstico somente leitura: nenhuma equipe foi restaurada, nenhum integrante iniciado e nenhuma ferramenta de integrante executada. A retomada exige comprovar a posse exclusiva da execução, restaurar as caixas dos integrantes e preservar a configuração original da equipe."

// ResumeInterrupted inspects checkpoint eligibility without mutating the
// journal, starting a teammate or invoking an executor. A journal prompt is
// never used as a substitute for a durable model/tool boundary.
func (sm *SwarmManager) ResumeInterrupted(name, member string) (TeamResumeReport, error) {
	name = strings.TrimSpace(name)
	member = strings.TrimSpace(member)
	report := TeamResumeReport{Team: name, Note: resumeDiagnosticNote}
	if name == "" {
		return report, errors.New("team é obrigatório")
	}
	if member == TeamLeaderName {
		return report, errors.New("o líder não é um integrante retomável")
	}
	sm.mu.RLock()
	_, activeHere := sm.teams[name]
	journal := sm.journal
	store := sm.checkpoints
	logger := sm.logger
	sm.mu.RUnlock()
	if activeHere {
		return report, fmt.Errorf("equipe %s ainda está ativa neste processo", name)
	}
	if journal == nil || store == nil {
		return report, errors.New("diagnóstico indisponível sem diário e checkpoint da equipe")
	}
	if logger == nil {
		logger = slog.Default()
	}
	archive, ok, err := journal.Team(name)
	if err != nil {
		return report, err
	}
	if !ok {
		return report, fmt.Errorf("equipe %s não encontrada", name)
	}
	report.RunID = archive.RunID
	members, err := diagnosticMembers(archive, member)
	if err != nil {
		return report, err
	}
	for _, info := range members {
		snapshot, issues, inspectErr := store.Inspect(name, archive.RunID, info.Name)
		if inspectErr != nil {
			logger.Warn("worker checkpoint unreadable", "team", name, "agent", info.Name, "error", inspectErr)
		}
		result := diagnoseMember(name, info, archive.Results[info.Name], snapshot, issues, inspectErr,
			archive.Summary.State == TeamStateStopped.String(), archive.ExternalOwner)
		report.Members = append(report.Members, result)
	}
	return report, nil
}

func diagnosticMembers(archive ArchivedTeam, only string) ([]MemberInfo, error) {
	members := make([]MemberInfo, 0, len(archive.Members))
	for _, info := range archive.Members {
		if info.Name == "" || info.Name == TeamLeaderName || only != "" && info.Name != only {
			continue
		}
		members = append(members, info)
	}
	if only != "" && len(members) == 0 {
		return nil, fmt.Errorf("integrante %s não encontrado na equipe %s", only, archive.Summary.Name)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	return members, nil
}

func diagnoseMember(team string, info MemberInfo, turns []ArchivedTurn, snap *workerCheckpointSnapshot,
	issues []workerCheckpointIssue, loadErr error, teamStopped, externalOwner bool,
) TeamResumeMember {
	result := TeamResumeMember{Agent: info.Name}
	for _, issue := range issues {
		entry := TeamResumeIssue{Turn: issue.Turn, PendingToolCallID: issue.PendingToolCallID}
		if issue.Err != nil {
			entry.Kind = "unreadable_checkpoint"
			result.Blockers = appendUniqueBlocker(result.Blockers, "checkpoint_unreadable")
		} else {
			entry.Kind = "pending_tool_effect_unknown"
			result.Blockers = appendUniqueBlocker(result.Blockers, "pending_tool_effect_unknown")
			if result.PendingToolCallID == "" {
				result.PendingToolCallID = issue.PendingToolCallID
			}
		}
		result.Issues = append(result.Issues, entry)
	}
	sort.Slice(result.Issues, func(i, j int) bool { return result.Issues[i].Turn < result.Issues[j].Turn })
	maxTurn, inFlight := journalTurnSpan(turns)
	if snap == nil {
		result.Turn = maxTurn
		result.Action = "no_checkpoint"
		result.Blockers = append(result.Blockers, "worker_mailbox_unverified")
		result.Reason = "nenhum checkpoint de turno encontrado; mensagens aguardando na caixa não foram verificadas"
		if maxTurn > 0 || inFlight || loadErr != nil || len(issues) != 0 {
			result.Action = "ineligible"
			result.Reason = "turno interrompido sem checkpoint íntegro; o prompt do diário não autoriza retomada"
		}
		return result
	}
	result.Turn = snap.Turn
	result.Phase = snap.Checkpoint.Phase
	if len(issues) != 0 {
		result.Action = "blocked"
		result.Reason = "há uma ferramenta de efeito incerto ou checkpoint ilegível neste ou em outro turno"
		return result
	}
	if snap.Team != team || snap.Agent != info.Name || snap.Identity.AgentName != info.Name ||
		snap.Identity.TeamName != team || snap.Identity.AgentID == "" || snap.Identity.AgentID != info.AgentID ||
		snap.Identity.PlanModeRequired != info.PlanModeRequired ||
		info.AgentType != "" && snap.AgentType != "" && info.AgentType != snap.AgentType {
		result.Action = "ineligible"
		result.Reason = "identidade ou tipo do checkpoint não corresponde ao integrante"
		return result
	}
	if maxTurn > snap.Turn {
		result.Turn = maxTurn
		result.Action = "ineligible"
		result.Reason = "há um turno mais recente sem checkpoint; o checkpoint anterior não pode ser usado"
		return result
	}
	if _, err := snap.Checkpoint.ResumeQuery(); err != nil {
		result.Action = "ineligible"
		result.Reason = "checkpoint sem prompt verificável; o diário não é usado como substituto"
		return result
	}
	if turnCleanlyFinished(turns, snap.Turn) {
		result.Action = "already_finished"
		result.Reason = "o turno já foi encerrado no diário"
		return result
	}
	if teamStopped {
		result.Action = "ineligible"
		result.Reason = "equipe encerrada; o turno não pode ser retomado"
		return result
	}
	if snap.Checkpoint.Phase == tasks.AgentCheckpointPhaseDone {
		result.Action = "checkpoint_done"
		result.Blockers = append(result.Blockers, "journal_finalization_disabled")
		result.Reason = "checkpoint concluído; a finalização no diário está desativada"
		return result
	}
	if err := snap.Checkpoint.ValidateResume(); err != nil || snap.AgentType == "" || snap.Turn < 1 {
		result.Action = "ineligible"
		result.Reason = "checkpoint sem fronteira retomável ou sem tipo de agente"
		return result
	}
	result.Action = "checkpoint_valid"
	result.Blockers = []string{"worker_mailbox_unverified", "team_config_unverified", "run_lease_unverified", "all_members_restore_unverified"}
	if externalOwner {
		result.Blockers = append(result.Blockers, "run_owned_by_other_process")
	}
	result.Reason = "fronteira do checkpoint íntegra; a retomada permanece desativada pelos bloqueios listados"
	return result
}

func appendUniqueBlocker(blockers []string, value string) []string {
	for _, blocker := range blockers {
		if blocker == value {
			return blockers
		}
	}
	return append(blockers, value)
}

func journalTurnSpan(turns []ArchivedTurn) (maxTurn int, inFlight bool) {
	for _, turn := range turns {
		if turn.Turn > maxTurn {
			maxTurn = turn.Turn
		}
		if turn.Running || turn.Interrupted {
			inFlight = true
		}
	}
	return maxTurn, inFlight
}

func turnCleanlyFinished(turns []ArchivedTurn, turnNumber int) bool {
	for _, turn := range turns {
		if turn.Turn == turnNumber && !turn.Running && !turn.Interrupted && !turn.Finished.IsZero() {
			return true
		}
	}
	return false
}
