import { describe, expect, it } from 'vitest';

import * as taxonomy from './api/generated/auditactions';
import * as namespaces from './api/generated/auditnamespaces';
import type { AuditEvent } from './api/types';
import {
	auditActionGroups,
	auditNamespaceLabels,
	auditFilterSearch,
	auditPhrasing,
	isIPAddress,
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

describe('isIPAddress', () => {
	it.each([
		['192.0.2.7', true],
		['0.0.0.0', true],
		['255.255.255.255', true],
		[' 192.0.2.7 ', true],
		['2001:db8::1', true],
		['::1', true],
		['::ffff:192.0.2.7', true],
		['', false],
		['   ', false],
		['192.0.2', false],
		['10.1', false],
		['192.0.2.256', false],
		['192.0.2.07', false],
		['192.0.2.7:22', false],
		['192.0.2.0/24', false],
		['2001:db8::/32', false],
		['[2001:db8::1]', false],
		['2001:db8:::1', false],
		['example.com', false]
	])('should judge %j an address: %s', (value, want) => {
		expect(isIPAddress(value)).toBe(want);
	});
});

describe('auditFilterSearch', () => {
	it.each([
		['', '', ''],
		['auth.login', '', '?action=auth.login'],
		['', '192.0.2.7', '?source_ip=192.0.2.7'],
		['cert.requested', '2001:db8::1', '?action=cert.requested&source_ip=2001%3Adb8%3A%3A1']
	])('should write action %j and source %j as %j', (action, source, want) => {
		expect(auditFilterSearch(action, source)).toBe(want);
	});
});

describe('auditActionGroups', () => {
	const all = Object.values(taxonomy).filter((v): v is string => typeof v === 'string');
	const offered = auditActionGroups(all).flatMap((group) => group.actions);

	it('should offer every action the feed can return', () => {
		const expected = all.filter(
			(action) => !hiddenAuditActions.includes(action) && action !== taxonomy.AuditCertIssued
		);
		expect([...offered].sort()).toEqual([...expected].sort());
	});

	it('should not offer an action the feed never returns', () => {
		expect(offered).not.toContain(taxonomy.AuditCertIssued);
	});

	it('should put certificates first', () => {
		expect(auditActionGroups(all)[0].label).toBe('Certificates');
	});

	it('should group an unknown namespace under its own name', () => {
		expect(auditActionGroups(['zeta.thing', 'auth.login']).map((g) => g.label)).toEqual([
			'Sign-in',
			'zeta'
		]);
	});

	it('should give no groups for an empty taxonomy', () => {
		expect(auditActionGroups([])).toEqual([]);
	});

	// Every namespace the server declares gets words of its own, so no group
	// in the filter is titled with a raw namespace name.
	const labelled = auditNamespaceLabels.map(([namespace]) => namespace);
	for (const namespace of Object.values(namespaces).filter(
		(v): v is string => typeof v === 'string'
	)) {
		it(`should label the ${namespace} namespace`, () => {
			expect(labelled).toContain(namespace);
		});
	}
});
