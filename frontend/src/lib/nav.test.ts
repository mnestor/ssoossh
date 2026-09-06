import { describe, expect, it } from 'vitest';

import { adminNav, isAdminRoute, isCurrent, isFocusRoute, primaryNav, accountNav } from './nav';

// Test methodology: table-driven over the path predicates, which are the
// only part of the model that can be wrong at runtime. The boundary cases
// are the point — a bare prefix match would light up the wrong rail item
// and would strip the rail from /console, and both bugs are invisible until
// somebody navigates there.

describe('isFocusRoute', () => {
	const cases: Array<{ name: string; pathname: string; want: boolean }> = [
		{ name: 'the sign-in page', pathname: '/login', want: true },
		{ name: 'an approval', pathname: '/approve/req-123', want: true },
		{ name: 'a console code', pathname: '/c/ABCD-1234', want: true },
		{ name: 'the approval-unavailable notice', pathname: '/approval-unavailable', want: true },
		{ name: 'the dashboard', pathname: '/dashboard', want: false },
		{ name: 'the admin area', pathname: '/admin/users', want: false },
		// "/console" starts with "/c" but is a signed-in destination that
		// must keep its rail.
		{ name: 'the console login page', pathname: '/console', want: false },
		{ name: 'a page merely starting with approve', pathname: '/approvers', want: false }
	];

	for (const { name, pathname, want } of cases) {
		it(`should ${want ? 'drop' : 'keep'} the rail on ${name}`, () => {
			expect(isFocusRoute(pathname)).toBe(want);
		});
	}
});

describe('isCurrent', () => {
	const cases: Array<{ name: string; href: string; pathname: string; want: boolean }> = [
		{ name: 'the section page itself', href: '/admin/users', pathname: '/admin/users', want: true },
		{
			name: 'a detail page beneath the section',
			href: '/admin/users',
			pathname: '/admin/users/user-123',
			want: true
		},
		{
			name: 'a sibling section',
			href: '/admin/users',
			pathname: '/admin/config',
			want: false
		},
		// A bare prefix match would mark Certificates on this path.
		{
			name: 'a path the section is only a prefix of',
			href: '/admin/certificates',
			pathname: '/admin/certificates-archive',
			want: false
		}
	];

	for (const { name, href, pathname, want } of cases) {
		it(`should ${want ? 'mark' : 'not mark'} ${name}`, () => {
			expect(isCurrent(href, pathname)).toBe(want);
		});
	}
});

describe('isAdminRoute', () => {
	it('should recognise a page inside the admin area', () => {
		expect(isAdminRoute('/admin/audit')).toBe(true);
	});

	it('should not recognise a page outside it', () => {
		expect(isAdminRoute('/dashboard')).toBe(false);
	});

	// The rail's admin group opens on arrival, so a path that merely starts
	// with the same letters must not open it.
	it('should not recognise a path the admin prefix only starts', () => {
		expect(isAdminRoute('/administration')).toBe(false);
	});
});

describe('nav model', () => {
	it('should name every admin section', () => {
		expect(adminNav.map((item) => item.label)).toEqual([
			'Users',
			'Certificates',
			'Service codes',
			'Config',
			'Directory',
			'Claims echo',
			'Audit log',
			'Diagnostics'
		]);
	});

	it('should give every primary destination an icon', () => {
		expect(primaryNav().every((item) => item.icon.length > 0)).toBe(true);
	});

	it('should give every admin destination an icon', () => {
		expect(adminNav.every((item) => item.icon.length > 0)).toBe(true);
	});

	// Two items sharing an href would leave the rail marking both as
	// current on the same page.
	it('should not repeat an href across the rail', () => {
		const hrefs = [...primaryNav(), ...adminNav, ...accountNav()].map((item) => item.href);

		expect(new Set(hrefs).size).toBe(hrefs.length);
	});
});
