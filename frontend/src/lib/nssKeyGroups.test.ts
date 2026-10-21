import { afterEach, describe, expect, it, vi } from 'vitest';

import { envelope, routeFetch } from '#lib/testing/nss.js';

import { keyGroupsPageSize, mergeGroupSuggestions, readKeyGroups } from './nssKeyGroups';

// Test methodology: the merge is pure, so a table pins it; the read is
// routed to a fixture page of keys, pinning what it asks for and what it
// takes from the answer.

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('mergeGroupSuggestions', () => {
	const cases: { name: string; keyGroups: string[]; ownGroups: string[]; want: string[] }[] = [
		{ name: 'both lists are empty', keyGroups: [], ownGroups: [], want: [] },
		{
			name: 'only other keys name groups',
			keyGroups: ['web-admins', 'db-admins'],
			ownGroups: [],
			want: ['db-admins', 'web-admins']
		},
		{
			name: 'only the reader names groups',
			keyGroups: [],
			ownGroups: ['ops', 'dev'],
			want: ['dev', 'ops']
		},
		{
			name: 'a group is in both lists',
			keyGroups: ['web-admins'],
			ownGroups: ['web-admins', 'ops'],
			want: ['ops', 'web-admins']
		},
		{
			name: 'several keys share one group',
			keyGroups: ['web-admins', 'web-admins', 'db-admins'],
			ownGroups: [],
			want: ['db-admins', 'web-admins']
		},
		{
			name: 'keys without a group send empty names',
			keyGroups: ['', 'db-admins', ''],
			ownGroups: [''],
			want: ['db-admins']
		},
		{ name: 'every name is empty', keyGroups: ['', ''], ownGroups: [''], want: [] }
	];

	for (const { name, keyGroups, ownGroups, want } of cases) {
		it(`should list each group once in order when ${name}`, () => {
			expect(mergeGroupSuggestions(keyGroups, ownGroups)).toEqual(want);
		});
	}
});

describe('readKeyGroups', () => {
	it('should ask for the first page of every key at the largest page size', async () => {
		const calls = routeFetch({
			'GET /api/admin/nss/keys': envelope({ keys: [], total: 0, usage_retention_seconds: 0 })
		});
		await readKeyGroups();

		expect(calls[0]?.url).toBe(`/api/admin/nss/keys?limit=${keyGroupsPageSize}`);
	});

	it('should return each key group as the server sent it when the read succeeds', async () => {
		routeFetch({
			'GET /api/admin/nss/keys': envelope({
				keys: [{ group_name: 'db-admins' }, { group_name: '' }],
				total: 2,
				usage_retention_seconds: 0
			})
		});

		expect(await readKeyGroups()).toEqual(['db-admins', '']);
	});

	it('should reject when the read fails', async () => {
		routeFetch({ 'GET /api/admin/nss/keys': envelope(null, 500, 'boom') });

		await expect(readKeyGroups()).rejects.toThrow();
	});
});
