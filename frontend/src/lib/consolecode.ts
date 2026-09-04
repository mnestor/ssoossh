import { ApiError } from '$lib/api/client';

// The browser half of the console login code: what the code-entry box does
// to input as it is typed, and what the three ways a submission can fail
// mean to the person who typed it.
//
// Normalization here is a courtesy, not the contract. The server normalizes
// again and its answer is the one that counts (see
// service.NormalizeUserCode), so this file can be generous without ever
// being the thing that decides whether a code is valid.

/**
 * Crockford Base32, mirroring service.userCodeAlphabet: digits plus the
 * upper-case letters minus I, L, O and U.
 */
const ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';

/** How many symbols a code carries, mirroring service.userCodeLength. */
export const CODE_LENGTH = 8;

/** How the display form groups them, mirroring service.userCodeGroupSize. */
const GROUP_SIZE = 4;

/**
 * normalizeCode keeps only what a code can contain, applying Crockford's
 * decoding aliases on the way: I and L become 1, O becomes 0.
 *
 * Anything else — the hyphen the display form carries, spaces from a
 * copy/paste, a letter that is not in the alphabet — is dropped rather than
 * flagged. Refusing a keystroke mid-word is the wrong feedback for someone
 * squinting at a serial console; the submit button and the server's answer
 * are where a wrong code is reported.
 */
export function normalizeCode(input: string): string {
	let out = '';
	for (const char of input.toUpperCase()) {
		if (char === 'I' || char === 'L') {
			out += '1';
		} else if (char === 'O') {
			out += '0';
		} else if (ALPHABET.includes(char)) {
			out += char;
		}
		if (out.length === CODE_LENGTH) {
			break;
		}
	}
	return out;
}

/**
 * formatCode groups a normalized code the way the console prints it, so what
 * someone reads off the screen and what they see in the box look the same.
 */
export function formatCode(code: string): string {
	const groups: string[] = [];
	for (let i = 0; i < code.length; i += GROUP_SIZE) {
		groups.push(code.slice(i, i + GROUP_SIZE));
	}
	return groups.join('-');
}

/** isComplete reports whether a normalized code is long enough to submit. */
export function isComplete(code: string): boolean {
	return code.length === CODE_LENGTH;
}

/**
 * Why a code submission failed, kept as a stable identifier so the e2e tier
 * selects on it rather than on prose (see test/e2e/README.md).
 *
 * The three server-side cases are deliberately distinct because they send
 * the user to three different next actions.
 */
export type CodeFailureKind = 'not-found' | 'expired' | 'claimed' | 'invalid' | 'unknown';

/** What the code-entry page renders when a submission fails. */
export interface CodeFailure {
	kind: CodeFailureKind;
	title: string;
	message: string;
}

/**
 * describeCodeError turns a failed resolve into something a human can act
 * on.
 *
 * 401 is deliberately absent: the caller redirects to /login before asking
 * for a description, the same way the approval page does.
 */
export function describeCodeError(error: unknown): CodeFailure {
	if (error instanceof ApiError) {
		if (error.isGone) {
			return {
				kind: 'expired',
				title: 'That login has expired',
				message:
					'Console logins are held open for a short time on purpose. Start the login again at the machine and a new code will appear.'
			};
		}
		if (error.isForbidden) {
			return {
				kind: 'claimed',
				title: 'Someone else already has this login',
				message:
					'A code can only be used once, by one account. If that was not you, treat it as a login you did not start: leave it, and tell whoever runs that machine.'
			};
		}
		if (error.isNotFound) {
			return {
				kind: 'not-found',
				title: 'No login is waiting on that code',
				message:
					'Check the characters against the screen — 1 and 0 are digits here, never I, L or O — or start the login again at the machine.'
			};
		}
		if (error.status === 400) {
			return {
				kind: 'invalid',
				title: 'That is not a complete code',
				message: `A code is ${CODE_LENGTH} characters, shown in two groups of ${GROUP_SIZE}.`
			};
		}
	}

	return {
		kind: 'unknown',
		title: 'Could not check that code',
		message: error instanceof Error ? error.message : 'something went wrong'
	};
}
