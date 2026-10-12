import { describe, expect, it } from 'vitest';

import { describeList, describeLoaded, type ListState } from './listStatus';

/** state builds a settled single-page list, overriding only what a case cares about. */
function state(overrides: Partial<ListState> = {}): ListState {
	return { noun: 'certificate', total: 12, loading: false, ready: true, ...overrides };
}

describe('describeList', () => {
	it('should announce nothing when the list has not loaded yet', () => {
		expect(describeList(state({ ready: false }))).toBe('');
	});

	it('should announce nothing while a request is in flight', () => {
		expect(describeList(state({ loading: true }))).toBe('');
	});

	it('should report the count when the list has settled', () => {
		expect(describeList(state())).toBe('12 certificates found.');
	});

	it('should use the singular noun when exactly one row matches', () => {
		expect(describeList(state({ total: 1 }))).toBe('1 certificate found.');
	});

	it('should say so when nothing matches', () => {
		expect(describeList(state({ total: 0 }))).toBe('No certificates found.');
	});

	it('should name the search term when one narrowed the list', () => {
		expect(describeList(state({ total: 3, query: 'alice' }))).toBe(
			'3 certificates found matching “alice”.'
		);
	});

	it('should name the search term on an empty result', () => {
		expect(describeList(state({ total: 0, query: 'nobody' }))).toBe(
			'No certificates found matching “nobody”.'
		);
	});

	it('should ignore a search term that is only whitespace', () => {
		expect(describeList(state({ query: '   ' }))).toBe('12 certificates found.');
	});

	it('should carry the caller’s noun through to the plural', () => {
		expect(describeList(state({ noun: 'user', total: 4 }))).toBe('4 users found.');
	});

	it('should handle a multi-word noun', () => {
		expect(describeList(state({ noun: 'audit event', total: 2 }))).toBe('2 audit events found.');
	});
});

describe('describeLoaded', () => {
	it.each([
		{
			name: 'should say only what is shown when everything is in',
			shown: 25,
			notLoaded: 0,
			remaining: 'older',
			want: '25 shown'
		},
		{
			name: 'should name the older rows still to load when a newest-first list has more',
			shown: 48,
			notLoaded: 120,
			remaining: 'older',
			want: '48 shown, 120 older not loaded'
		},
		{
			name: 'should say more rather than older when the list is not ordered by time',
			shown: 25,
			notLoaded: 3,
			remaining: 'more',
			want: '25 shown, 3 more not loaded'
		},
		{
			name: 'should say nothing is left when the count to load is negative',
			shown: 2,
			notLoaded: -1,
			remaining: 'more',
			want: '2 shown'
		},
		{
			name: 'should read zero shown when nothing is on screen',
			shown: 0,
			notLoaded: 0,
			remaining: 'older',
			want: '0 shown'
		}
	] as const)('$name', ({ shown, notLoaded, remaining, want }) => {
		expect(describeLoaded(shown, notLoaded, remaining)).toBe(want);
	});
});
