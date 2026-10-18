import { vi } from 'vitest';

import type { NSSRotationBucket } from '#lib/api/generated/enums.js';
import type {
	CurrentUser,
	NSSAccountDetail,
	NSSAccountSummary,
	NSSGroupDetail,
	NSSGroupSummary,
	NSSKey,
	NSSRotationReport,
	NSSRotationReportRow
} from '#lib/api/types.js';

// Fixtures and a fetch router shared by the NSS page tests. Wire values are
// literal on purpose: they pin what the server sends.

/** userFixture is a signed-in identity with no roles and NSS on, overridable per test. */
export function userFixture(overrides: Partial<CurrentUser> = {}): CurrentUser {
	return {
		subject: 'sub-alice',
		username: 'alice',
		name: 'Alice Ashworth',
		email: 'alice@corp.example',
		groups: ['web-admins'],
		other_accounts: [],
		service_accounts: [],
		approvable_service_accounts: [],
		user_own_service_accounts: [],
		extra: {},
		is_admin: false,
		is_soc: false,
		is_auditor: false,
		is_nss_key_admin: false,
		nss_enabled: true,
		posix: null,
		detail_level: 'advanced',
		...overrides
	};
}

/** keyFixture is an active key never rotated, which the caller may only view. */
export function keyFixture(overrides: Partial<NSSKey> = {}): NSSKey {
	return {
		id: 'nss_k7m4qp2x',
		name: 'web fleet',
		description: 'every host in the web tier',
		group_name: 'web-admins',
		status: 'active',
		current_generation: 1,
		created_by: 'user-1',
		created_by_username: 'bob',
		created_at: '2026-01-10T12:00:00Z',
		updated_at: '2026-01-10T12:00:00Z',
		expires_at: '2027-01-10T12:00:00Z',
		notification_email: 'web-admins@corp.example',
		permissions: ['view'],
		...overrides
	};
}

/** reportRow is one address in a rotation report. */
export function reportRow(
	source_ip: string,
	bucket: NSSRotationBucket,
	overrides: Partial<NSSRotationReportRow> = {}
): NSSRotationReportRow {
	return {
		source_ip,
		bucket,
		first_seen: '2026-09-01T00:00:00Z',
		last_seen: '2026-10-17T00:00:00Z',
		lookups: 100,
		last_generation: bucket === 'switched' ? 2 : 1,
		...overrides
	};
}

/** reportFixture is a report after one rotation, its counts taken from the rows. */
export function reportFixture(
	rows: NSSRotationReportRow[],
	overrides: Partial<NSSRotationReport> = {}
): NSSRotationReport {
	const count = (bucket: string) => rows.filter((row) => row.bucket === bucket).length;
	return {
		key_id: 'nss_k7m4qp2x',
		current_generation: 2,
		rotated_at: '2026-10-10T12:00:00Z',
		previous_expires_at: '2026-11-09T12:00:00Z',
		grace_days_left: 22,
		switched: count('switched'),
		not_switched: count('not_switched'),
		gone: count('gone'),
		rows,
		...overrides
	};
}

/** accountSummary is one stored account as the list sends it, active. */
export function accountSummary(
	name: string,
	uid: number,
	overrides: Partial<NSSAccountSummary> = {}
): NSSAccountSummary {
	return {
		id: `acct-${uid}`,
		name,
		uid,
		gid: 5000,
		gecos: `${name} user`,
		dn: `uid=${name},ou=People,dc=corp,dc=example`,
		state: 'active',
		last_synced_at: '2026-10-18T09:00:00Z',
		...overrides
	};
}

/** groupSummary is one stored group as the list sends it, active. */
export function groupSummary(
	name: string,
	gid: number,
	overrides: Partial<NSSGroupSummary> = {}
): NSSGroupSummary {
	return {
		id: `grp-${gid}`,
		name,
		gid,
		dn: `cn=${name},ou=Groups,dc=corp,dc=example`,
		state: 'active',
		member_count: 3,
		last_synced_at: '2026-10-18T09:00:00Z',
		...overrides
	};
}

/** accountFixture is bob, stored, active and served, in one stored group. */
export function accountFixture(overrides: Partial<NSSAccountDetail> = {}): NSSAccountDetail {
	return {
		id: 'acct-10001',
		name: 'bob',
		uid: 10001,
		gid: 5000,
		directory_id: '7f3c2a10-0000-4000-8000-000000000001',
		dn: 'uid=bob,ou=People,dc=corp,dc=example',
		home: '/home/bob',
		shell: '/bin/bash',
		gecos: 'Bob Baker',
		dir_disabled: false,
		person: { user_id: 'user-bob', username: 'bob', display_name: 'Bob Baker' },
		state: 'active',
		created_at: '2026-09-01T00:00:00Z',
		updated_at: '2026-10-17T00:00:00Z',
		last_synced_at: '2026-10-18T09:00:00Z',
		groups: [{ gid: 5100, name: 'ops', state: 'active' }],
		served: {
			name: 'bob',
			uid: 10001,
			gid: 5000,
			groups: [
				{ gid: 5000, name: 'staff' },
				{ gid: 5100, name: 'ops' }
			],
			home: '/home/bob',
			shell: '/bin/bash',
			gecos: 'Bob Baker',
			status: 'active'
		},
		...overrides
	};
}

/** groupFixture is ops, stored, active and served with every member. */
export function groupFixture(overrides: Partial<NSSGroupDetail> = {}): NSSGroupDetail {
	return {
		id: 'grp-5100',
		name: 'ops',
		gid: 5100,
		directory_id: '7f3c2a10-0000-4000-8000-000000000101',
		dn: 'cn=ops,ou=Groups,dc=corp,dc=example',
		state: 'active',
		member_count: 2,
		created_at: '2026-09-01T00:00:00Z',
		updated_at: '2026-10-17T00:00:00Z',
		last_synced_at: '2026-10-18T09:00:00Z',
		served: { name: 'ops', gid: 5100, member_count: 2, members_truncated: false },
		...overrides
	};
}

/** envelope builds a Response carrying the API's {data, error} envelope. */
export function envelope(data: unknown, status = 200, error: string | null = null): Response {
	return new Response(JSON.stringify({ data: status < 400 ? data : null, error }), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

/** A recorded call: method, path after /api, and the decoded body. */
export interface Call {
	method: string;
	url: string;
	body: unknown;
}

/** A route answers one request, by `METHOD path` with the query string kept. */
export type Route = (call: Call) => Response | Promise<Response>;

/**
 * routeFetch stubs fetch with a table of routes keyed "METHOD /api/path"
 * (query string included when the key has one, ignored otherwise). An
 * unmatched request is a 599, so a test that reaches one fails loudly.
 */
export function routeFetch(routes: Record<string, Route | Response | (() => Response)>): Call[] {
	const calls: Call[] = [];
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: string, init?: RequestInit) => {
			const method = init?.method ?? 'GET';
			const call: Call = {
				method,
				url: input,
				body: init?.body ? JSON.parse(String(init.body)) : undefined
			};
			calls.push(call);
			const path = input.split('?')[0];
			const route = routes[`${method} ${input}`] ?? routes[`${method} ${path}`];
			if (route === undefined) {
				return envelope(null, 599, `no route for ${method} ${input}`);
			}
			if (route instanceof Response) {
				return route.clone();
			}
			return (route as Route)(call);
		})
	);
	return calls;
}

/** settle lets pending promise chains and effects run. */
export function settle(): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, 0));
}
