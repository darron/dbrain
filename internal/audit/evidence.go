package audit

type EvidenceKind string

const (
	EvidenceInteger        EvidenceKind = "integer"
	EvidenceBoolean        EvidenceKind = "boolean"
	EvidenceTimestamp      EvidenceKind = "timestamp"
	EvidenceEnum           EvidenceKind = "enum"
	EvidenceDaily          EvidenceKind = "daily"
	EvidenceByKind         EvidenceKind = "by_kind"
	EvidenceMissingByField EvidenceKind = "missing_by_field"
)

type ErrorCode string

const (
	ErrorUnavailable          ErrorCode = "unavailable"
	ErrorTimeout              ErrorCode = "timeout"
	ErrorCanceled             ErrorCode = "canceled"
	ErrorInterrupted          ErrorCode = "interrupted"
	ErrorRead                 ErrorCode = "read_error"
	ErrorParse                ErrorCode = "parse_error"
	ErrorBudgetExhausted      ErrorCode = "budget_exhausted"
	ErrorConfiguration        ErrorCode = "configuration_error"
	ErrorCredentialResolution ErrorCode = "credential_resolution_error"
	ErrorDestinationRejected  ErrorCode = "destination_rejected"
	ErrorListingIncomplete    ErrorCode = "listing_incomplete"
	ErrorManifest             ErrorCode = "manifest_error"
	ErrorDatabase             ErrorCode = "database_error"
)

func (e ErrorCode) Valid() bool {
	switch e {
	case ErrorUnavailable, ErrorTimeout, ErrorCanceled, ErrorInterrupted, ErrorRead, ErrorParse,
		ErrorBudgetExhausted, ErrorConfiguration, ErrorCredentialResolution, ErrorDestinationRejected,
		ErrorListingIncomplete, ErrorManifest, ErrorDatabase:
		return true
	default:
		return false
	}
}
