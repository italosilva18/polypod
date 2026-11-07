package scheduler

import (
	"errors"
	"os"
	"path/filepath"
)

const defaultRoutineZone = "America/Sao_Paulo"

// EnsureDefaultRoutines creates a small operational cadence on first startup.
// A private marker records that defaults have been seeded. Existing tasks are
// left untouched; edits, disabling and removal survive future restarts.
func EnsureDefaultRoutines(s *Scheduler) (created int, err error) {
	if s == nil {
		return 0, errors.New("scheduler: rotina padrao exige agendador")
	}
	if s.dataFile == "" {
		return 0, errors.New("scheduler: arquivo de estado obrigatorio para rotinas padrao")
	}
	marker := s.dataFile + ".defaults-seeded"
	if _, err := os.Stat(marker); err == nil {
		return 0, nil
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	for _, spec := range []Task{
		{
			ID: "system_daily_plan", Name: "Planejamento diário", Schedule: "0 9 * * *",
			TimeZone: defaultRoutineZone, Channel: "system", UserID: "owner",
			MisfirePolicy: "run_once", MaxRetries: 1,
			Prompt: "Planejamento das 09h. Revise metas, rotinas, tarefas pendentes, caixa de entrada e compromissos disponíveis. Defina até três prioridades concretas para hoje e avance ações prontas e autorizadas. Registre resultados; use ask_user quando faltar uma informação essencial. Informe ao dono apenas decisões ou bloqueios relevantes. Se nada exigir ação ou aviso, responda exatamente NO_REPLY.",
		},
		{
			ID: "system_daily_review", Name: "Revisão diária", Schedule: "0 18 * * *",
			TimeZone: defaultRoutineZone, Channel: "system", UserID: "owner",
			MisfirePolicy: "run_once", MaxRetries: 1,
			Prompt: "Revisão das 18h. Confira metas, histórico das rotinas, tarefas concluídas e falhas da caixa de entrada. Registre o que foi comprovado, o que falhou e o próximo passo para amanhã. Faça ações prontas e autorizadas; avise ao dono somente pendências ou resultados relevantes. Se nada exigir ação ou aviso, responda exatamente NO_REPLY.",
		},
		{
			ID: "system_weekly_review", Name: "Revisão semanal", Schedule: "0 10 * * 1",
			TimeZone: defaultRoutineZone, Channel: "system", UserID: "owner",
			MisfirePolicy: "run_once", MaxRetries: 1,
			Prompt: "Revisão de segunda às 10h. Examine metas, histórico de tarefas, falhas, memória consolidada e padrões verificados da semana anterior. Atualize prioridades e proponha melhorias concretas de rotina ou skill com evidência. Avance ações prontas e autorizadas; use ask_user quando faltar uma decisão do dono. Se nada exigir ação ou aviso, responda exatamente NO_REPLY.",
		},
	} {
		if _, exists := s.GetTask(spec.ID); exists {
			continue
		}
		copy := spec
		if err := s.AddTask(&copy); err != nil {
			return created, err
		}
		created++
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return created, err
	}
	if err := os.WriteFile(marker+".tmp", []byte("v1\n"), 0o600); err != nil {
		return created, err
	}
	if err := os.Rename(marker+".tmp", marker); err != nil {
		return created, err
	}
	return created, nil
}
