package account

type Profile string

const (
	ProfileCLI Profile = "cli"
	ProfileIDE Profile = "ide"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusBanned   Status = "banned"
	StatusCooling  Status = "cooling"
)

type AcquireHint struct {
	Profile  Profile
	Model    string
	Protocol string
	Stream   bool
}

type Lease struct {
	AccountID string
	Token     string
	Profile   Profile
	Metadata  map[string]string
}

type SuccessMeta struct {
	RequestID                string
	Model                    string
	Tokens                   int
	InputTokens              int
	OutputTokens             int
	Credits                  float64
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	Attempts                 int
}

type FailureReason string

const (
	FailureReasonAuth           FailureReason = "auth_error"
	FailureReasonQuota          FailureReason = "quota_error"
	FailureReasonBan            FailureReason = "ban_error"
	FailureReasonNetwork        FailureReason = "network_error"
	FailureReasonNotImplemented FailureReason = "not_implemented"
	FailureReasonUnknown        FailureReason = "unknown_error"
)

type FailureMeta struct {
	RequestID  string
	Model      string
	Reason     FailureReason
	BodySignal string
	StatusCode int
	Message    string
	Attempts   int
}

type Record struct {
	ID      string
	Profile Profile
	Enabled bool
	Weight  int
	Status  Status
	Labels  map[string]string
}
