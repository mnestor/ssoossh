/**
 * What the sign-in callback's ?error= reasons mean to the person reading
 * them. The server sends a sign-in that did not complete back to /login,
 * and a claims echo that did not complete back to the echo page, with one
 * of these reasons (server/controller/auth.go, signInFailure) instead of
 * rendering an error body the browser would show raw.
 */
export type SignInFailureReason = 'expired' | 'refused' | 'session' | 'failed';

/** SignInFailure is one reason, worded for the page that shows it. */
export interface SignInFailure {
	reason: SignInFailureReason;
	title: string;
	message: string;
	/** Whether the next step is someone else's: an operator, not a retry. */
	needsSupport: boolean;
}

/**
 * describeSignInFailure turns a ?error= value into its wording, or null
 * when there is none. An unrecognized value reads as the general failure
 * rather than as nothing: something sent the browser back saying it failed.
 */
export function describeSignInFailure(reason: string | null | undefined): SignInFailure | null {
	if (!reason) {
		return null;
	}
	switch (reason) {
		case 'expired':
			return {
				reason,
				title: 'That sign-in had expired',
				message:
					'It took too long, or it was started in another tab or browser. Sign in again to continue.',
				needsSupport: false
			};
		case 'refused':
			return {
				reason,
				title: 'Your identity provider did not sign you in',
				message:
					'It came back without completing the sign-in, for example because it was cancelled. Sign in again to retry.',
				needsSupport: false
			};
		case 'session':
			return {
				reason,
				title: 'Signed in, but the session could not be saved',
				message:
					'Your account is fine. This server could not store the session, so trying again will not help until an administrator fixes it.',
				needsSupport: true
			};
		default:
			return {
				reason: 'failed',
				title: 'Sign-in did not complete',
				message: 'Sign in again. If it keeps failing, contact support.',
				needsSupport: true
			};
	}
}
