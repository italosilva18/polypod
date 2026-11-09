package tasks

import (
	"fmt"
	"io"
	"os"
)

// ReadTaskOutput reads only the tail of a task log, even when the log is large.
func ReadTaskOutput(registry *TaskRegistry, taskID string, maxBytes int64) (string, error) {
	state, ok := registry.Get(taskID)
	if !ok {
		return "", fmt.Errorf("tarefa %s nao encontrada", taskID)
	}
	if state.OutputFile == "" {
		return "", nil
	}
	file, err := os.Open(state.OutputFile)
	if err != nil {
		return "", fmt.Errorf("abrindo saida da tarefa: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("consultando saida da tarefa: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	start := int64(0)
	if info.Size() > maxBytes {
		start = info.Size() - maxBytes
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", fmt.Errorf("posicionando saida da tarefa: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return "", fmt.Errorf("lendo saida da tarefa: %w", err)
	}
	if start > 0 {
		return "... [inicio omitido]\n" + string(data), nil
	}
	return string(data), nil
}
