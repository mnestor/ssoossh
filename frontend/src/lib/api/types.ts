// The app-facing names for the ssoosshd wire contract.
//
// Every shape here is generated from the Go structs that produce it — see
// tygo.yaml and `make types`. This file adds no fields and invents no types;
// it exists so the app can say `RequestDetail` instead of
// `RequestDetailResponse`, and so there is one place to look when a Go name
// and a TypeScript name differ.
//
// If you are adding a field, add it in Go and run `make types`. Editing
// anything under generated/ is pointless: the next run overwrites it, and
// server/webtypes/golden_test.go fails the build if the two disagree.

export type { CertificateType, CertificateRequestStatus as RequestStatus } from './generated/enums';

export type {
	CertificateListResponse,
	CertificateOptionsResponse as CertificateOptions,
	CertificateResponse as CertificateRecord,
	CurrentUserResponse as CurrentUser,
	NotificationKindResponse as NotificationKind,
	NotificationPreferencesResponse as NotificationPreferences,
	PageMeta,
	RequestDetailResponse as RequestDetail,
	EnrollmentRetrievalsResponse,
	ServiceEnrollmentResponse as ServiceEnrollment,
	ServiceEnrollmentsResponse
} from './generated/webtypes';

export type {
	ApproveResponse as ApproveResult,
	DenyResponse as DenyResult
} from './generated/apitypes';

/**
 * The {data, error} envelope every JSON response carries.
 *
 * Deliberately NOT the generated `apitypes.Envelope`, which is the Go
 * client's decode target and describes success alone: its `Data T` is
 * non-nullable and its `Error` is omitted when empty. What ssoosshd actually
 * puts on the wire is looser at both ends — a success writes
 * `{"data": …, "error": null}` and a failure writes `{"data": null,
 * "error": "…"}` (server/controller/responses.go and
 * server/middleware/error_handler.go) — so a browser that trusted the Go
 * struct would be wrong on every error response.
 *
 * Hand-written for that reason, and kept honest by the envelope assertions
 * in server/controller/webapi_test.go rather than by generation.
 */
export interface Envelope<T> {
	data: T | null;
	error?: string | null;
}
