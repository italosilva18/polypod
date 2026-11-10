package swarm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/costa/polypod/internal/tasks"
)

// ResumeExecutorFactory resolves the saved agent type without executing it.
type ResumeExecutorFactory func(MemberInfo) (TeammateExecutor, error)

type resumedEvidence struct {
	runID          string
	summary        TeamSummary
	config         TeamConfig
	members        []MemberInfo
	pending        map[string][]*Message
	maxTurn        map[string]int
	inspectNames   []string
	completed      map[string][]int
	historical     map[string]MemberInfo
	maxDeliverySeq uint64
}

type resumeMessageKey struct {
	target string
	id     string
}

type resumeTurnKey struct {
	agent string
	turn  int
}

// ApplyResumeInterrupted restores a run created with the durable delivery
// protocol. Every started turn must have a durable finish. A turn with an
// uncertain tool result is never replayed; the whole run stays blocked.
// Older runs and runs that were already resumed require manual inspection.
func (sm *SwarmManager) ApplyResumeInterrupted(name string, factory ResumeExecutorFactory) (TeamResumeReport, error) {
	report := TeamResumeReport{Team: name, Note: "Retomada não executada."}
	if name == "" || factory == nil {
		return report, errors.New("equipe e fábrica de executores são obrigatórias")
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if _, exists := sm.teams[name]; exists {
		return report, fmt.Errorf("equipe %s já está ativa neste processo", name)
	}
	if sm.journal == nil || sm.checkpoints == nil {
		return report, errors.New("retomada exige diário e checkpoints persistentes")
	}
	teamLock, err := sm.checkpoints.lockTeam(name)
	if err != nil {
		return report, err
	}
	transferred := false
	defer func() {
		if !transferred {
			releaseResumeLock(teamLock)
		}
	}()
	ownerID, err := readTeamOwner(teamLock)
	if err != nil {
		report.Note = "Marcador de posse ausente ou ilegível; nenhum trabalho foi reiniciado."
		return report, nil
	}
	sm.journal.mu.Lock()
	records, err := sm.journal.latestRecordsLocked()
	sm.journal.mu.Unlock()
	if err != nil {
		return report, err
	}
	evidence, err := collectResumeEvidence(name, records)
	if err != nil {
		report.Note = err.Error() + "; nenhum trabalho foi reiniciado."
		return report, nil
	}
	report.RunID = evidence.runID
	if ownerID != evidence.runID {
		report.Note = "O marcador de posse indica outra execução da equipe; nenhum trabalho foi reiniciado."
		return report, nil
	}
	for _, agentName := range evidence.inspectNames {
		if agentName == TeamLeaderName {
			continue
		}
		_, issues, inspectErr := sm.checkpoints.Inspect(name, evidence.runID, agentName)
		if inspectErr != nil || len(issues) != 0 {
			report.Note = "Checkpoint ilegível ou efeito de ferramenta incerto; nenhum integrante foi reiniciado."
			return report, nil
		}
	}
	for agentName, turns := range evidence.completed {
		info, known := evidence.historical[agentName]
		if !known || info.AgentType == "" {
			report.Note = "Identidade histórica de integrante incompleta; nenhum trabalho foi reiniciado."
			return report, nil
		}
		for _, turn := range turns {
			snapshot, readErr := sm.checkpoints.ReadTurn(name, evidence.runID, agentName, turn)
			if readErr != nil || snapshot == nil || snapshot.Checkpoint == nil ||
				snapshot.Checkpoint.Phase != tasks.AgentCheckpointPhaseDone ||
				snapshot.Checkpoint.PendingToolCallID != "" || snapshot.AgentType != info.AgentType ||
				snapshot.Checkpoint.AgentName != info.AgentType ||
				snapshot.Identity.AgentID != info.AgentID ||
				snapshot.Identity.PlanModeRequired != info.PlanModeRequired {
				report.Note = fmt.Sprintf("Turno %d de %s sem checkpoint final íntegro; nenhum trabalho foi reiniciado.", turn, agentName)
				return report, nil
			}
		}
	}
	resumeLock, err := sm.checkpoints.lockRun(name, evidence.runID)
	if err != nil {
		return report, err
	}
	defer func() {
		if !transferred {
			releaseResumeLock(resumeLock)
		}
	}()
	executors := make(map[string]TeammateExecutor)
	for _, info := range evidence.members {
		if info.Name == TeamLeaderName {
			continue
		}
		executor, resolveErr := factory(info)
		if resolveErr != nil || executor == nil {
			report.Note = fmt.Sprintf("Executor do integrante %s indisponível; nenhum trabalho foi reiniciado.", info.Name)
			return report, nil
		}
		executors[info.Name] = executor
	}
	team, err := restoredTeam(sm, evidence, teamLock, resumeLock, executors)
	if err != nil {
		return report, err
	}
	// The resume marker is durable before any worker goroutine can read its
	// restored inbox. A crash before this append leaves every turn unstarted.
	if err := sm.journal.append(journalEvent{Kind: "resumed", Team: name, RunID: evidence.runID,
		Summary: ptrSummary(team.Summary())}); err != nil {
		team.cancel()
		return report, fmt.Errorf("persisting team resume: %w", err)
	}
	sm.teams[name] = team
	transferred = true
	for _, runner := range team.runners {
		runner.Start()
	}
	report.Continued = true
	report.Note = "Equipe restaurada. Só mensagens comprovadamente não iniciadas voltaram às caixas; turnos anteriores não foram repetidos."
	for _, info := range evidence.members {
		if info.Name == TeamLeaderName {
			continue
		}
		item := TeamResumeMember{Agent: info.Name, Action: "resumed_idle", Reason: "integrante aguardando novas mensagens"}
		if count := len(evidence.pending[info.Name]); count > 0 {
			item.Action = "resumed_pending"
			item.Reason = fmt.Sprintf("%d mensagem(ns) duráveis não iniciadas foram restauradas", count)
		}
		report.Members = append(report.Members, item)
	}
	return report, nil
}

func collectResumeEvidence(name string, records []journalRecord) (resumedEvidence, error) {
	var created *journalEvent
	var runID string
	for _, record := range records {
		event := record.event
		if event.Team != name || event.Kind != "created" {
			continue
		}
		if created != nil || event.Summary == nil || event.Config == nil ||
			event.ResumeProtocol != teamResumeProtocolVersion {
			return resumedEvidence{}, errors.New("diário antigo ou criação ambígua; retomada automática bloqueada")
		}
		copy := event
		created = &copy
		runID = event.RunID
	}
	if created == nil {
		return resumedEvidence{}, errors.New("equipe não encontrada em diário compatível")
	}
	state := created.Summary.State
	members := make(map[string]MemberInfo)
	historical := make(map[string]MemberInfo)
	seenMemberNames := make(map[string]bool)
	var memberOrder []string
	messages := make(map[resumeMessageKey]*Message)
	deliverySeqs := make(map[string]map[uint64]bool)
	var maxDeliverySeq uint64
	started := make(map[resumeTurnKey]journalEvent)
	finished := make(map[resumeTurnKey]bool)
	acked := make(map[string]bool)
	for _, record := range records {
		event := record.event
		if event.Team != name {
			continue
		}
		if event.RunID != runID {
			return resumedEvidence{}, errors.New("execução da equipe incompleta ou ambígua")
		}
		if event.Kind == "created" || event.Kind == "resumed" {
			if record.ownerLive {
				return resumedEvidence{}, errors.New("outra instância ainda possui a execução")
			}
			if record.ownerUnproven {
				return resumedEvidence{}, errors.New("lock do proprietário ausente; encerramento da instância não comprovado")
			}
		}
		switch event.Kind {
		case "created":
		case "state":
			if event.Summary == nil {
				return resumedEvidence{}, errors.New("estado da equipe ilegível")
			}
			state = event.Summary.State
		case "stopping":
			return resumedEvidence{}, errors.New("encerramento da equipe já foi solicitado")
		case "member":
			if event.Member == nil || event.Member.Name == "" || event.Member.AgentID == "" {
				return resumedEvidence{}, errors.New("integrante sem identidade durável")
			}
			if seenMemberNames[event.Member.Name] {
				return resumedEvidence{}, errors.New("nome de integrante reutilizado no diário; identidade da caixa é ambígua")
			}
			seenMemberNames[event.Member.Name] = true
			members[event.Member.Name] = *event.Member
			historical[event.Member.Name] = *event.Member
			memberOrder = append(memberOrder, event.Member.Name)
		case "member_removed":
			if _, exists := members[event.Agent]; !exists || event.Agent == TeamLeaderName {
				return resumedEvidence{}, errors.New("remoção de integrante ambígua")
			}
			delete(members, event.Agent)
		case "message":
			if event.Message == nil || event.Message.ID == "" || event.Message.Target == "" ||
				event.Message.DeliverySeq == 0 || event.Message.DeliverySeq >= 1<<63 {
				return resumedEvidence{}, errors.New("mensagem sem identidade durável")
			}
			if event.Message.DeliverySeq > maxDeliverySeq {
				maxDeliverySeq = event.Message.DeliverySeq
			}
			if deliverySeqs[event.Message.Target] == nil {
				deliverySeqs[event.Message.Target] = make(map[uint64]bool)
			}
			if deliverySeqs[event.Message.Target][event.Message.DeliverySeq] {
				return resumedEvidence{}, errors.New("ordem de entrega duplicada na caixa")
			}
			deliverySeqs[event.Message.Target][event.Message.DeliverySeq] = true
			key := resumeMessageKey{target: event.Message.Target, id: event.Message.ID}
			if _, exists := messages[key]; exists {
				return resumedEvidence{}, errors.New("mensagem duplicada no diário")
			}
			messages[key] = cloneMessage(event.Message)
		case "ack":
			for _, id := range event.IDs {
				acked[id] = true
			}
		case "turn_started":
			key := resumeTurnKey{agent: event.Agent, turn: event.Turn}
			if event.Agent == "" || event.Turn < 1 || event.MessageID == "" || started[key].Kind != "" {
				return resumedEvidence{}, errors.New("turno iniciado sem vínculo único à mensagem")
			}
			started[key] = event
		case "turn_finished":
			key := resumeTurnKey{agent: event.Agent, turn: event.Turn}
			if event.Agent == "" || event.Turn < 1 || finished[key] {
				return resumedEvidence{}, errors.New("finalização de turno ambígua")
			}
			finished[key] = true
		case "resumed", "turn_resumed":
			return resumedEvidence{}, errors.New("execução já foi retomada antes; nova retomada automática bloqueada")
		default:
			return resumedEvidence{}, fmt.Errorf("evento desconhecido no diário: %s", event.Kind)
		}
	}
	if runID == "" || created.Summary.Name != name || state != TeamStateRunning.String() {
		return resumedEvidence{}, errors.New("equipe não está em uma execução contínua comprovada")
	}
	if leader, ok := members[TeamLeaderName]; !ok || leader.AgentID != FormatAgentID(TeamLeaderName, name) {
		return resumedEvidence{}, errors.New("líder da equipe não foi preservado integralmente")
	}
	for key, start := range started {
		if !finished[key] {
			return resumedEvidence{}, fmt.Errorf("turno %d de %s começou e tem resultado incerto", key.turn, key.agent)
		}
		if _, ok := messages[resumeMessageKey{target: key.agent, id: start.MessageID}]; !ok {
			return resumedEvidence{}, errors.New("turno sem mensagem durável correspondente")
		}
	}
	for key := range finished {
		if _, ok := started[key]; !ok {
			return resumedEvidence{}, errors.New("resultado sem início de turno comprovado")
		}
	}
	consumed := make(map[resumeMessageKey]bool)
	maxTurn := make(map[string]int)
	completed := make(map[string][]int)
	for key, start := range started {
		if key.turn >= int(^uint32(0)>>1) {
			return resumedEvidence{}, errors.New("contador de turnos excede o limite do executor")
		}
		if key.turn > maxTurn[key.agent] {
			maxTurn[key.agent] = key.turn
		}
		completed[key.agent] = append(completed[key.agent], key.turn)
		messageKey := resumeMessageKey{target: key.agent, id: start.MessageID}
		if consumed[messageKey] {
			return resumedEvidence{}, errors.New("mensagem associada a mais de um turno")
		}
		consumed[messageKey] = true
	}
	for agentName, highest := range maxTurn {
		if len(completed[agentName]) != highest {
			return resumedEvidence{}, errors.New("sequência de turnos incompleta no diário")
		}
	}
	evidence := resumedEvidence{runID: runID, summary: *created.Summary, config: *created.Config,
		pending: make(map[string][]*Message), maxTurn: maxTurn, inspectNames: memberOrder,
		completed: completed, historical: historical, maxDeliverySeq: maxDeliverySeq}
	seenAgentIDs := make(map[string]bool, len(members))
	for _, memberName := range memberOrder {
		if info, ok := members[memberName]; ok {
			if info.AgentID != FormatAgentID(info.Name, name) {
				return resumedEvidence{}, errors.New("identidade do integrante diverge do diário")
			}
			if seenAgentIDs[info.AgentID] {
				return resumedEvidence{}, errors.New("identificador de integrante duplicado no diário")
			}
			seenAgentIDs[info.AgentID] = true
			evidence.members = append(evidence.members, info)
		}
	}
	for key, msg := range messages {
		if key.target == TeamLeaderName && acked[key.id] || consumed[key] {
			continue
		}
		if _, exists := members[key.target]; !exists {
			return resumedEvidence{}, fmt.Errorf("mensagem pendente de integrante removido: %s", key.target)
		}
		if msg.Type == MessageShutdownRequest || msg.Type == MessageShutdownApproved ||
			msg.Type == MessageShutdownRejected || msg.Type == MessagePermissionRequest ||
			msg.Type == MessagePermissionResponse {
			return resumedEvidence{}, errors.New("mensagem de controle pendente exige inspeção manual")
		}
		evidence.pending[key.target] = append(evidence.pending[key.target], msg)
		if len(evidence.pending[key.target]) > maxPendingMessages {
			return resumedEvidence{}, errors.New("caixa restaurada excede a capacidade")
		}
	}
	for target := range evidence.pending {
		sort.Slice(evidence.pending[target], func(i, k int) bool {
			a, b := evidence.pending[target][i], evidence.pending[target][k]
			if a.DeliverySeq != b.DeliverySeq {
				return a.DeliverySeq < b.DeliverySeq
			}
			return a.ID < b.ID
		})
	}
	return evidence, nil
}

func restoredTeam(sm *SwarmManager, evidence resumedEvidence, teamLock, resumeLock *os.File,
	executors map[string]TeammateExecutor,
) (*Team, error) {
	ctx, cancel := context.WithCancel(context.Background())
	name := evidence.summary.Name
	router := NewMessageRouter(name, sm.logger)
	router.journal, router.runID = sm.journal, evidence.runID
	team := &Team{Name: name, Description: evidence.summary.Description, CreatedAt: evidence.summary.CreatedAt,
		Config: evidence.config, State: TeamStateRunning, LeaderID: evidence.summary.LeaderID,
		LeaderName: TeamLeaderName, Members: evidence.members, router: router, cancel: cancel, ctx: ctx,
		runners: make(map[string]*TeammateRunner), retiredNames: make(map[string]bool),
		logger: sm.logger, journal: sm.journal,
		checkpoints: sm.checkpoints, runID: evidence.runID, teamLock: teamLock, resumeLock: resumeLock}
	active := make(map[string]bool, len(evidence.members))
	for _, member := range evidence.members {
		active[member.Name] = true
	}
	for _, historicalName := range evidence.inspectNames {
		if !active[historicalName] {
			team.retiredNames[strings.ToLower(historicalName)] = true
		}
	}
	team.perms = NewPermissionBridge(team, sm.logger)
	advanceMessageSequenceFloor(evidence.maxDeliverySeq)
	for _, member := range evidence.members {
		router.Register(member.Name)
	}
	for target, pending := range evidence.pending {
		mailbox := router.GetMailbox(target)
		if mailbox == nil {
			cancel()
			return nil, errors.New("caixa de integrante não encontrada")
		}
		for _, msg := range pending {
			if err := mailbox.Deliver(cloneMessage(msg)); err != nil {
				cancel()
				return nil, fmt.Errorf("restoring worker mailbox: %w", err)
			}
		}
	}
	for _, info := range evidence.members {
		if info.Name == TeamLeaderName {
			continue
		}
		identity := TeammateIdentity{AgentID: info.AgentID, AgentName: info.Name, TeamName: name,
			Color: info.Color, PlanModeRequired: info.PlanModeRequired}
		cfg := RunnerConfig{Identity: identity, Mailbox: router.GetMailbox(info.Name), Router: router,
			Executor: executors[info.Name], TokenBudget: evidence.config.DefaultTokenBudget,
			TurnTimeout: evidence.config.DefaultTimeout, Logger: sm.logger, Journal: sm.journal,
			RunID: evidence.runID, AgentType: info.AgentType, Checkpoints: sm.checkpoints}
		team.runners[info.Name] = NewTeammateRunner(ctx, cfg)
		atomic.StoreInt32(&team.runners[info.Name].turnCount, int32(evidence.maxTurn[info.Name]))
	}
	return team, nil
}
