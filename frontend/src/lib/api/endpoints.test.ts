import { afterEach, describe, expect, it, vi } from 'vitest';

import {
	acceptNSSGroupIDChange,
	acceptNSSUserIDChange,
	approveRequest,
	clearNSSKeyPrevious,
	createNSSKey,
	getNSSAccount,
	getNSSCollisions,
	getNSSGroup,
	getNSSKey,
	getNSSKeySettings,
	getNSSKeyUsage,
	getNSSPendingIDChanges,
	getNSSRotationReport,
	listAdminNSSKeys,
	listNSSAccounts,
	listNSSGroupMembers,
	listNSSGroups,
	listNSSKeys,
	nssRotationReportCSVURL,
	purgeNSSGroupTombstone,
	purgeNSSUserTombstone,
	revokeNSSKey,
	rotateNSSKey,
	updateNSSKey,
	denyRequest,
	getRequestDetail,
	listServiceEnrollments,
	loginURL,
	logout
} from './endpoints';

/** okFetch installs a fetch that answers everything with an empty envelope.
 * Typed as the real fetch so recorded calls carry its argument types. */
function okFetch() {
	const fetchMock = vi.fn<typeof fetch>(() =>
		Promise.resolve(
			new Response(JSON.stringify({ data: null, error: null }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			})
		)
	);
	vi.stubGlobal('fetch', fetchMock);
	return fetchMock;
}

/** firstCall returns the URL and init of the first recorded fetch call. */
function firstCall(fetchMock: ReturnType<typeof okFetch>) {
	const [input, init] = fetchMock.mock.calls[0] ?? [];
	return { url: String(input), init: init ?? {} };
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('loginURL', () => {
	it('should carry a relative path as return_to', () => {
		expect(loginURL('/approve/abc')).toBe('/auth/login?return_to=%2Fapprove%2Fabc');
	});

	it('should omit return_to when none is given', () => {
		expect(loginURL()).toBe('/auth/login');
	});

	// The server re-validates this, but sending something it will refuse
	// would silently drop the user on / after login instead.
	it('should refuse an absolute URL as return_to', () => {
		expect(loginURL('https://evil.example/steal')).toBe('/auth/login');
	});

	it('should refuse a protocol-relative URL as return_to', () => {
		expect(loginURL('//evil.example/steal')).toBe('/auth/login');
	});

	it('should refuse an empty return_to', () => {
		expect(loginURL('')).toBe('/auth/login');
	});
});

describe('request-id encoding', () => {
	it('should percent-encode the request id in the detail path', async () => {
		const fetchMock = okFetch();
		await getRequestDetail('a/../b');
		expect(firstCall(fetchMock).url).toBe('/api/certs/requests/a%2F..%2Fb');
	});

	it('should percent-encode the request id in the approve path', async () => {
		const fetchMock = okFetch();
		await approveRequest('a b');
		expect(firstCall(fetchMock).url).toBe('/api/certs/requests/a%20b/approve');
	});

	it('should post rather than get when denying', async () => {
		const fetchMock = okFetch();
		await denyRequest('abc');
		expect(firstCall(fetchMock).init.method).toBe('POST');
	});
});

describe('listServiceEnrollments', () => {
	it('should get the enrollment list under the api prefix', async () => {
		const fetchMock = okFetch();
		await listServiceEnrollments();
		expect(firstCall(fetchMock).url).toBe('/api/certs/service/enrollments');
	});
});

describe('logout', () => {
	// Outside /api because the auth routes are browser redirects rather than
	// JSON calls — the /api prefix would 404.
	it('should post to the auth route rather than the api prefix', async () => {
		const fetchMock = okFetch();
		await logout();
		expect(firstCall(fetchMock).url).toBe('/auth/logout');
	});

	it('should raise the status when logout fails', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.resolve(new Response(null, { status: 500 })))
		);
		await expect(logout()).rejects.toMatchObject({ status: 500 });
	});

	it('should report a network failure as status zero', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new TypeError('Failed to fetch')))
		);
		await expect(logout()).rejects.toMatchObject({ status: 0 });
	});
});

describe('NSS endpoints', () => {
	// Method and path per wrapper, pinned against the routes registered in
	// server/controller/nss_keys.go, nssadmin.go and users.go.
	const cases: { name: string; call: () => Promise<unknown>; method: string; url: string }[] = [
		{
			name: 'key settings',
			call: () => getNSSKeySettings(),
			method: 'GET',
			url: '/api/nss/key-settings'
		},
		{ name: 'list keys', call: () => listNSSKeys(), method: 'GET', url: '/api/nss/keys' },
		{
			name: 'one key',
			call: () => getNSSKey('nss_a/b'),
			method: 'GET',
			url: '/api/nss/keys/nss_a%2Fb'
		},
		{
			name: 'create a key',
			call: () => createNSSKey({ name: 'web' }),
			method: 'POST',
			url: '/api/nss/keys'
		},
		{
			name: 'change a key',
			call: () => updateNSSKey('nss_k1', { description: 'x' }),
			method: 'PATCH',
			url: '/api/nss/keys/nss_k1'
		},
		{
			name: 'rotate a key',
			call: () => rotateNSSKey('nss_k1'),
			method: 'POST',
			url: '/api/nss/keys/nss_k1/rotate'
		},
		{
			name: 'clear the previous secret',
			call: () => clearNSSKeyPrevious('nss_k1'),
			method: 'DELETE',
			url: '/api/nss/keys/nss_k1/previous'
		},
		{
			name: 'revoke a key',
			call: () => revokeNSSKey('nss_k1'),
			method: 'POST',
			url: '/api/nss/keys/nss_k1/revoke'
		},
		{
			name: 'usage, first page',
			call: () => getNSSKeyUsage('nss_k1'),
			method: 'GET',
			url: '/api/nss/keys/nss_k1/usage'
		},
		{
			name: 'usage, a later page',
			call: () => getNSSKeyUsage('nss_k1', { after: 'c 1' }),
			method: 'GET',
			url: '/api/nss/keys/nss_k1/usage?after=c+1'
		},
		{
			name: 'rotation report',
			call: () => getNSSRotationReport('nss_k1'),
			method: 'GET',
			url: '/api/nss/keys/nss_k1/rotation-report'
		},
		{
			name: 'pending id changes',
			call: () => getNSSPendingIDChanges(),
			method: 'GET',
			url: '/api/admin/nss/pending-id-changes'
		},
		{
			name: 'accept a uid',
			call: () => acceptNSSUserIDChange(10001),
			method: 'POST',
			url: '/api/admin/nss/users/10001/accept-id-change'
		},
		{
			name: 'accept a gid',
			call: () => acceptNSSGroupIDChange(5000),
			method: 'POST',
			url: '/api/admin/nss/groups/5000/accept-id-change'
		},
		{
			name: 'collisions',
			call: () => getNSSCollisions(),
			method: 'GET',
			url: '/api/admin/nss/collisions'
		},
		{
			name: 'purge an account tombstone',
			call: () => purgeNSSUserTombstone(10001),
			method: 'DELETE',
			url: '/api/admin/nss/users/10001/tombstone'
		},
		{
			name: 'purge a group tombstone',
			call: () => purgeNSSGroupTombstone(5000),
			method: 'DELETE',
			url: '/api/admin/nss/groups/5000/tombstone'
		},
		{
			name: 'list every fleet key',
			call: () => listAdminNSSKeys(),
			method: 'GET',
			url: '/api/admin/nss/keys'
		},
		{
			name: 'search and filter every fleet key',
			call: () =>
				listAdminNSSKeys({
					q: '10.0.4.0/24',
					status: 'active',
					expiringWithin: 30,
					unused: true,
					alerts: true,
					after: 'nss_k7m4qp2x',
					limit: 50
				}),
			method: 'GET',
			url: '/api/admin/nss/keys?q=10.0.4.0%2F24&status=active&expiring_within=30&unused=1&alerts=1&after=nss_k7m4qp2x&limit=50'
		},
		{
			name: 'list the stored accounts',
			call: () => listNSSAccounts(),
			method: 'GET',
			url: '/api/admin/nss/users'
		},
		{
			name: 'search the stored accounts',
			call: () => listNSSAccounts({ q: 'bob smith', state: 'withheld', after: 'c1', limit: 25 }),
			method: 'GET',
			url: '/api/admin/nss/users?q=bob+smith&state=withheld&after=c1&limit=25'
		},
		{
			name: 'read one stored account',
			call: () => getNSSAccount(10001),
			method: 'GET',
			url: '/api/admin/nss/users/10001'
		},
		{
			name: 'list the stored groups',
			call: () => listNSSGroups({ limit: 1 }),
			method: 'GET',
			url: '/api/admin/nss/groups?limit=1'
		},
		{
			name: 'search the stored groups',
			call: () => listNSSGroups({ q: '5000', state: 'tombstone' }),
			method: 'GET',
			url: '/api/admin/nss/groups?q=5000&state=tombstone'
		},
		{
			name: 'read one stored group',
			call: () => getNSSGroup(5000),
			method: 'GET',
			url: '/api/admin/nss/groups/5000'
		},
		{
			name: "page a group's members",
			call: () => listNSSGroupMembers(5000, { after: '10001', limit: 50 }),
			method: 'GET',
			url: '/api/admin/nss/groups/5000/members?after=10001&limit=50'
		}
	];

	for (const { name, call, method, url } of cases) {
		it(`should call ${method} ${url} when asked to ${name}`, async () => {
			const fetchMock = okFetch();
			await call();
			const { url: got, init } = firstCall(fetchMock);
			expect({ url: got, method: init.method }).toEqual({ url, method });
		});
	}

	it('should build the CSV address under the api prefix when asked for a download', () => {
		expect(nssRotationReportCSVURL('nss_k1')).toBe(
			'/api/nss/keys/nss_k1/rotation-report?format=csv'
		);
	});
});
