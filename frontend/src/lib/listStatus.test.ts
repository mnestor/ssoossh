import { describe, expect, it } from 'vitest';

import { describeList, type ListState } from './listStatus';

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

	it('should add the page position when the list runs to more than one page', () => {
		expect(describeList(state({ total: 120, page: 3, pageCount: 5 }))).toBe(
			'120 certificates found. Page 3 of 5.'
		);
	});

	it('should omit the page position when everything fits on one page', () => {
		expect(describeList(state({ page: 1, pageCount: 1 }))).toBe('12 certificates found.');
	});

	it('should omit the page position when the list is not paged', () => {
		expect(describeList(state({ page: undefined, pageCount: undefined }))).toBe(
			'12 certificates found.'
		);
	});

	it('should carry the caller’s noun through to the plural', () => {
		expect(describeList(state({ noun: 'user', total: 4 }))).toBe('4 users found.');
	});

	it('should handle a multi-word noun', () => {
		expect(describeList(state({ noun: 'audit event', total: 2 }))).toBe('2 audit events found.');
	});
});
