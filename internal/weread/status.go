package weread

// Status distinguishes a confirmed import from one whose result is unknown.
type Status string

const (
	StatusReady         Status = "ready"
	StatusLoginRequired Status = "login_required"
	StatusManual        Status = "manual"
	StatusSending       Status = "sending"
	StatusImported      Status = "imported"
	StatusPending       Status = "pending"
	StatusFailed        Status = "failed"
)
