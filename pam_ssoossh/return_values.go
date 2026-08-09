package main

// // The user was not authenticated
// const PAM_AUTH_ERR = 9

// // For some reason the application does not have sufficient credentials to authenticate the user.
// const PAM_CRED_INSUFFICIENT = 11

// // The modules were not able to access the authentication information. This might be due to a network or hardware failure etc.
// const PAM_AUTHINFO_UNAVAIL = 12

// // The supplied username is not known to the authentication service
// const PAM_USER_UNKNOWN = 13

// // One or more of the authentication modules has reached its limit of tries authenticating the user. Do not try again.
// const PAM_MAXTRIES = 8

// // Success
// const PAM_SUCCESS = 0

// PAM return codes (from <security/pam_appl.h>).

const (
	// Successful function return.
	PamSuccess = 0

	// dlopen() failure when dynamically loading a service module.
	PamOpenErr = 1

	// Symbol not found.
	PamSymbolErr = 2

	// Error in service module.
	PamServiceErr = 3

	// System error.
	PamSystemErr = 4

	// Memory buffer error.
	PamBufErr = 5

	// Permission denied.
	PamPermDenied = 6

	// Authentication failure.
	PamAuthErr = 7

	// Cannot access authentication data due to insufficient credentials.
	PamCredInsufficient = 8

	// Underlying authentication service cannot retrieve authentication information.
	PamAuthInfoUnavail = 9

	// User not known to the underlying authentication module.
	PamUserUnknown = 10

	// Authentication retry count reached; no further retries should be attempted.
	PamMaxTries = 11

	// New authentication token required (e.g., password change required).
	PamNewAuthtokReqd = 12

	// User account has expired.
	PamAcctExpired = 13

	// Cannot make/remove an entry for the specified session.
	PamSessionErr = 14

	// Underlying authentication service cannot retrieve user credentials.
	PamCredUnavail = 15

	// User credentials expired.
	PamCredExpired = 16

	// Failure setting user credentials.
	PamCredErr = 17

	// No module specific data is present.
	PamNoModuleData = 18

	// Conversation (I/O) error.
	PamConvErr = 19

	// Authentication token manipulation error.
	PamAuthtokErr = 20

	// Authentication information cannot be recovered.
	PamAuthtokRecoveryErr = 21

	// Authentication token lock busy.
	PamAuthtokLockBusy = 22

	// Authentication token aging disabled.
	PamAuthtokDisableAging = 23

	// Preliminary check by password service; try again.
	PamTryAgain = 24

	// Ignore underlying account module regardless of control flag.
	PamIgnore = 25

	// Critical error (module abort).
	PamAbort = 26

	// User's authentication token has expired.
	PamAuthtokExpired = 27

	// Module is not known.
	PamModuleUnknown = 28

	// Bad item passed to pam_*_item().
	PamBadItem = 29

	// Conversation function is event driven and data is not available yet.
	PamConvAgain = 30

	// Call again to complete authentication stack after conversation completes.
	PamIncomplete = 31
)
