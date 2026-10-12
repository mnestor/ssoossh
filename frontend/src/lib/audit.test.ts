import { describe, expect, it } from 'vitest';

import * as taxonomy from './api/generated/auditactions';
import type { AuditEvent } from './api/types';
import {
	auditPhrasing,
	dedupeAuditEvents,
	describeAuditAction,
	hiddenAuditActions,
	visibleAuditEvents
} from './audit';

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

// Every action the server can record, from the tygo output of
// server/service/audit_actions.go. A new action there without an entry in
// auditPhrasing fails here by name, so it cannot ship rendering as its raw
// action string. Run `make types` after adding one on the Go side.
const serverActions = Object.values(taxonomy) as string[];

describe('auditPhrasing', () => {
	it('should see the server taxonomy when the generated list is loaded', () => {
		// Guards the walk below: an empty or renamed module would otherwise
		// pass every case by having none.
		expect(serverActions).toContain('auth.login');
	});

	it.each(serverActions)('should phrase %s when the server records it', (action) => {
		expect(
			Object.hasOwn(auditPhrasing, action),
			`audit action "${action}" has no phrasing: add it to auditPhrasing in frontend/src/lib/audit.ts`
		).toBe(true);
	});
});

describe('describeAuditAction', () => {
	it.each([
		['cert.claimed', 'opened a certificate approval link'],
		['cert.expired', 'expired an unanswered certificate request'],
		['cert.sign_failed', 'could not sign an approved certificate'],
		['admin.diagnostics_run', 'ran the deployment diagnostics'],
		// The taxonomy grows without a wire change: an action this build
		// does not know shows its raw name rather than vanishing.
		['future.thing', 'future.thing'],
		['', ''],
		// An inherited property name is not a phrase.
		['constructor', 'constructor']
	])('should describe %s as %s', (action, want) => {
		expect(describeAuditAction(action)).toBe(want);
	});
});
