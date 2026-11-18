package download

// TaskAction is an operation applied independently to each selected task.
// File deletion requires an explicit action distinct from record removal.
type TaskAction string

const (
	TaskActionPause         TaskAction = "pause"
	TaskActionResume        TaskAction = "resume"
	TaskActionRetry         TaskAction = "retry"
	TaskActionRemoveRecords TaskAction = "remove-records"
	TaskActionRemoveFiles   TaskAction = "remove-files"
)

// TaskActionResult reports the outcome of one operation. Removal can delete
// the record even if a subsequent attempt to delete its files fails.
type TaskActionResult struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
	// FileCleanupError is the filesystem error after successful record removal.
	FileCleanupError string `json:"fileCleanupError,omitempty"`
}
