/**
 * Audit feed display rules, kept out of the components so the timeline and
 * the page that counts its rows cannot disagree about what is shown.
 */

import type { AuditEvent } from '$lib/api/types';

/**
 * Actions the web UI does not render.
 *
 * These are the privileged-view events: opening a user record, or reading
 * the log itself. The server no longer writes either to the table the UI
 * reads (see service.tableSkipped) — they go to the shipped archive, where
 * "who looked at this account" is still a real question — but rows recorded
 * before that change stay until the retention sweep clears them, and one per
 * screen an auditor opens is exactly what buries the decisions this view
 * exists to show.
 */
export const hiddenAuditActions = ['admin.audit_viewed', 'admin.user_viewed'];

/**
 * visibleAuditEvents drops the hidden actions, and tolerates a response that
 * is not an array at all: an unexpected shape must render "nothing to show"
 * rather than tearing down the page the timeline is embedded in.
 */
export function visibleAuditEvents(events: AuditEvent[]): AuditEvent[] {
	if (!Array.isArray(events)) {
		return [];
	}
	return events.filter((event) => !hiddenAuditActions.includes(event.action));
}

/**
 * dedupeAuditEvents keeps the first occurrence of each event id, preserving
 * order.
 *
 * Offset paging over a live table cannot promise the window holds still: any
 * event written between two requests shifts every row down one, and the last
 * row of the first page arrives again as the first row of the second. The
 * timeline keys its {#each} by id, so a repeat is not a cosmetic duplicate —
 * it throws each_key_duplicate and takes the page down.
 *
 * A plain object rather than a Set: svelte/prefer-svelte-reactivity rejects a
 * built-in Set in a component, and the collection is scratch — rebuilt on
 * every call and discarded once the array is returned.
 */
export function dedupeAuditEvents(events: AuditEvent[]): AuditEvent[] {
	const seen: Record<string, true> = {};
	const out: AuditEvent[] = [];
	for (const event of events) {
		if (seen[event.id]) {
			continue;
		}
		seen[event.id] = true;
		out.push(event);
	}
	return out;
}
