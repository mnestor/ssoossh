import { describe, expect, it } from 'vitest';

import type { AuditEvent } from './api/types';
import { dedupeAuditEvents, hiddenAuditActions, visibleAuditEvents } from './audit';

/** event builds a minimal audit row for these tests. */
function event(id: string, action: string): AuditEvent {
	return {
		id,
		action,
		created_at: '2026-08-14T09:00:00Z',
		system: false
	} as AuditEvent;
}

describe('visibleAuditEvents', () => {
	it('should keep an ordinary decision event', () => {
		expect(visibleAuditEvents([event('a', 'user.disabled')])).toHaveLength(1);
	});

	// One row per user record an auditor opens buries the decisions the view
	// exists to show.
	for (const action of hiddenAuditActions) {
		it(`should drop ${action}`, () => {
			expect(visibleAuditEvents([event('a', action)])).toHaveLength(0);
		});
	}

	it('should keep the surviving rows in their original order', () => {
		const rows = visibleAuditEvents([
			event('a', 'auth.login'),
			event('b', 'admin.user_viewed'),
			event('c', 'cert.approved')
		]);
		expect(rows.map((row) => row.id)).toEqual(['a', 'c']);
	});

	it('should return nothing for an empty feed', () => {
		expect(visibleAuditEvents([])).toEqual([]);
	});

	// A response shape the page did not expect must render "nothing to show"
	// rather than tearing down the page the timeline sits in.
	it('should return nothing when handed something that is not an array', () => {
		expect(visibleAuditEvents(null as unknown as AuditEvent[])).toEqual([]);
	});
});

describe('dedupeAuditEvents', () => {
	// The each block is keyed by id, so a repeat throws each_key_duplicate
	// rather than merely rendering twice.
	it('should keep only the first row carrying a repeated id', () => {
		const rows = dedupeAuditEvents([
			event('a', 'auth.login'),
			event('b', 'cert.approved'),
			event('a', 'auth.login')
		]);
		expect(rows.map((row) => row.id)).toEqual(['a', 'b']);
	});

	it('should leave a list with no repeats untouched', () => {
		const rows = dedupeAuditEvents([event('a', 'auth.login'), event('b', 'cert.approved')]);
		expect(rows.map((row) => row.id)).toEqual(['a', 'b']);
	});

	it('should return nothing for an empty list', () => {
		expect(dedupeAuditEvents([])).toEqual([]);
	});
});
