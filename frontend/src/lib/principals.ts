import { browser } from '$app/env';

/**
 * Remembered principals: the accounts an approver last approved with, kept
 * per signed-in user in this browser so the picker can pre-tick them next
 * time (DESIGN.md, "Remembered principals").
 *
 * A remembered selection pre-fills; it never approves. The chips stay on
 * the page at both detail levels, and unticking one on the next approval is
 * the way to forget it. Every principal is rememberable, break-glass ones
 * included.
 *
 * Kept per request type under the one key: a PAM or console certificate's
 * principals are matched against the local account on that host, so the
 * selection that suits a user certificate is rarely the one a sudo needs.
 */

/** The request types whose approval offers a principal picker. */
export type PickerType = 'user' | 'pam' | 'console';

/** storageKey is where one user's remembered principals live. */
export function storageKey(username: string): string {
	return `ssoossh:principals:${username}`;
}

/** isStringList narrows a parsed value to a list of strings. */
function isStringList(value: unknown): value is string[] {
	return Array.isArray(value) && value.every((entry) => typeof entry === 'string');
}

/** readAll returns everything stored for a user, or nothing when what is
 * stored is missing, unreadable or not the shape written here. */
function readAll(username: string): Partial<Record<PickerType, string[]>> {
	if (!browser) {
		return {};
	}
	try {
		const raw = localStorage.getItem(storageKey(username));
		if (!raw) {
			return {};
		}
		const parsed: unknown = JSON.parse(raw);
		if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
			return {};
		}
		const stored: Partial<Record<PickerType, string[]>> = {};
		for (const type of ['user', 'pam', 'console'] as const) {
			const list = (parsed as Record<string, unknown>)[type];
			if (isStringList(list)) {
				stored[type] = list;
			}
		}
		return stored;
	} catch {
		// Site data blocked, or not JSON: nothing remembered.
		return {};
	}
}

/** rememberedPrincipals returns what this user last approved a request of
 * this type with, or an empty list. */
export function rememberedPrincipals(username: string, type: PickerType): string[] {
	return readAll(username)[type] ?? [];
}

/** rememberPrincipals stores what this user just approved a request of this
 * type with. Called on Approve only. */
export function rememberPrincipals(username: string, type: PickerType, principals: string[]): void {
	if (!browser) {
		return;
	}
	try {
		const stored = readAll(username);
		stored[type] = principals;
		localStorage.setItem(storageKey(username), JSON.stringify(stored));
	} catch {
		// Site data blocked: nothing is remembered, and the approval stands.
	}
}

/**
 * preselect decides what the picker starts with: the remembered principals
 * the request offers, in the order it offers them, or the fallback when none
 * of them is on offer (nothing stored, or the accounts have changed).
 */
export function preselect(remembered: string[], offered: string[], fallback: string[]): string[] {
	const kept = offered.filter((principal) => remembered.includes(principal));
	return kept.length > 0 ? kept : fallback;
}
