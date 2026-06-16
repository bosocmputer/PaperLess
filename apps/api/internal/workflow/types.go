// Package workflow implements the core PaperLess signing rules (see docs/domain.md).
// This package must never import gin, net/http, or any HTTP framework — it is
// pure business logic so it can be tested without a server.
package workflow

import "time"

// ConditionType mirrors the CHECK constraint in signature_tasks.condition_type.
type ConditionType int16

const (
	ConditionAnyOne  ConditionType = 1
	ConditionAll     ConditionType = 2
	ConditionExternal ConditionType = 3
)

// TaskStatus mirrors the CHECK constraint in signature_tasks.status.
type TaskStatus string

const (
	TaskWaiting   TaskStatus = "waiting"
	TaskOpen      TaskStatus = "open"
	TaskSigned    TaskStatus = "signed"
	TaskSkipped   TaskStatus = "skipped"
	TaskCancelled TaskStatus = "cancelled"
	TaskRejected  TaskStatus = "rejected"
)

// DocStatus mirrors the CHECK constraint in documents.status.
type DocStatus string

const (
	DocImported  DocStatus = "imported"
	DocPending   DocStatus = "pending"
	DocRejected  DocStatus = "rejected"
	DocCompleted DocStatus = "completed"
	DocCancelled DocStatus = "cancelled"
)

// Task is a lightweight projection of signature_tasks used by the engine.
type Task struct {
	ID            int64
	DocumentID    int64
	WorkflowStepID int64
	AssignedUserID *int64
	ExternalSignerID *int64
	SequenceNo    int
	ConditionType ConditionType
	Status        TaskStatus
	Version       int
	RequestID     string // last request_id that touched this task (for idempotency)
}

// SignInput is the request from a signer to sign their task.
type SignInput struct {
	TaskID           int64
	SignerUserID     int64
	SignatureImageHash string
	Comment          string
	ConsentText      string
	IPAddress        string
	UserAgent        string
	SessionID        string
	RequestID        string
}

// RejectInput is the request from a signer to reject a document at their task.
type RejectInput struct {
	TaskID       int64
	SignerUserID  int64
	Reason       string
	IPAddress    string
	UserAgent    string
	RequestID    string
}

// StepProgress describes how far through a step we are.
type StepProgress struct {
	SequenceNo    int           `json:"sequence_no"`
	ConditionType ConditionType `json:"condition_type"`
	SignedCount   int           `json:"signed_count"`
	TotalCount    int           `json:"total_count"`
	Complete      bool          `json:"complete"`
}

// ErrStepAlreadyActioned is returned when a condition-1 step was completed by
// another signer. It is NOT a system error — the caller surfaces it as an
// informational message to the late signer.
type ErrStepAlreadyActioned struct {
	TaskID int64
}

func (e ErrStepAlreadyActioned) Error() string {
	return "ขั้นตอนนี้มีผู้ดำเนินการแล้ว"
}

// ErrDuplicateRequest is returned when the same request_id was already processed.
type ErrDuplicateRequest struct {
	RequestID string
}

func (e ErrDuplicateRequest) Error() string {
	return "duplicate request: " + e.RequestID
}

// ErrExternalTokenExpired is returned when an external-signer token has expired.
type ErrExternalTokenExpired struct {
	ExpiresAt time.Time
}

func (e ErrExternalTokenExpired) Error() string {
	return "external signer token has expired"
}
