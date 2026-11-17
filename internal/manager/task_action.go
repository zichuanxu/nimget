package manager

import (
	"context"
	"fmt"

	"github.com/amemiya02/nimget/internal/download"
)

// ApplyTaskAction applies an action once per distinct ID, in input order.
// Each operation uses the same state and filesystem checks as a single task.
// A task failure does not undo successful operations or interrupt the rest.
func (m *Manager) ApplyTaskAction(ctx context.Context, action download.TaskAction, ids []string) ([]download.TaskActionResult, error) {
	var apply func(context.Context, string) error
	switch action {
	case download.TaskActionPause:
		apply = m.Pause
	case download.TaskActionResume:
		apply = m.Resume
	case download.TaskActionRetry:
		apply = m.Retry
	case download.TaskActionRemoveRecords, download.TaskActionRemoveFiles:
		apply = func(ctx context.Context, id string) error {
			return m.Delete(ctx, id, action == download.TaskActionRemoveFiles)
		}
	default:
		return nil, fmt.Errorf("unsupported download task action %q", action)
	}

	results := make([]download.TaskActionResult, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		result := download.TaskActionResult{ID: id}
		if err := apply(ctx, id); err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results, nil
}
