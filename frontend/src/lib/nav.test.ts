import { describe, expect, it } from 'vitest';

import {
	currentHref,
	adminNav,
	adminNavFor,
	isAdminRoute,
	isCurrent,
	isFocusRoute,
	nssKeysNav,
	primaryNav,
	accountNav
} from './nav';

// Test methodology: table-driven over the path predicates, which are the
// only part of the model that can be wrong at runtime. The boundary cases
// are the point — a bare prefix match would light up the wrong rail item
// and would strip the rail from /console, and both bugs are invisible until
// somebody navigates there.

describe('isFocusRoute', () => {
	const cases: Array<{ name: string; pathname: string; want: boolean }> = [
		{ name: 'the sign-in page', pathname: '/login', want: true },
		{ name: 'a console code', pathname: '/c/ABCD-1234', want: true },
		{ name: 'the approval-unavailable notice', pathname: '/approval-unavailable', want: true },
		{ name: 'the dashboard', pathname: '/dashboard', want: false },
		{ name: 'the admin area', pathname: '/admin/users', want: false },
		// "/console" starts with "/c" but is a signed-in destination that
		// must keep its rail.
		{ name: 'the console login page', pathname: '/console', want: false },
		{ name: 'a page merely starting with approve', pathname: '/approvers', want: false },
		// Reaching an approval means signing in first, so its reader is
		// inside the app and keeps the app's chrome.
		{ name: 'an approval', pathname: '/approve/req-123', want: false }
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
			'NSS directory',
			'NSS keys',
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

describe('adminNavFor', () => {
	it('should offer every admin section when the viewer is an admin and NSS is on', () => {
		expect(adminNavFor(true, true)).toEqual(adminNav);
	});

	// Issues #136 and #161, and SSOOSSH.md for Config: SOC and auditors are
	// refused Config, the Directory, Claims echo and Diagnostics outright, so
	// the rail must not offer them.
	const adminOnlyHrefs = [
		'/admin/config',
		'/admin/directory',
		'/admin/identity/echo',
		'/admin/diagnostics'
	];

	for (const href of adminOnlyHrefs) {
		it(`should leave out ${href} when the viewer is not an admin`, () => {
			expect(adminNavFor(false).map((item) => item.href)).not.toContain(href);
		});

		it(`should offer ${href} when the viewer is an admin`, () => {
			expect(adminNavFor(true).map((item) => item.href)).toContain(href);
		});
	}

	it('should keep every section not marked admin-only when the viewer is not an admin', () => {
		expect(adminNavFor(false, true)).toEqual(adminNav.filter((item) => !item.adminOnly));
	});

	it('should mark only Config, the Directory, Claims echo and Diagnostics as admin-only', () => {
		expect(adminNav.filter((item) => item.adminOnly).map((item) => item.href)).toEqual(
			adminOnlyHrefs
		);
	});
});

// The NSS directory viewer is read at auditor level, so every admin-area
// role is offered it, but only where the server serves NSS.
describe('adminNavFor and the NSS directory', () => {
	const cases: { name: string; isAdmin: boolean; nssEnabled: boolean; want: boolean }[] = [
		{ name: 'an admin with NSS on', isAdmin: true, nssEnabled: true, want: true },
		{ name: 'SOC or an auditor with NSS on', isAdmin: false, nssEnabled: true, want: true },
		{ name: 'an admin with NSS off', isAdmin: true, nssEnabled: false, want: false },
		{ name: 'SOC or an auditor with NSS off', isAdmin: false, nssEnabled: false, want: false }
	];

	for (const { name, isAdmin, nssEnabled, want } of cases) {
		it(`should ${want ? 'offer' : 'leave out'} the NSS directory when the viewer is ${name}`, () => {
			expect(adminNavFor(isAdmin, nssEnabled).some((item) => item.href === '/admin/nss')).toBe(
				want
			);
		});
	}

	it('should leave out the NSS directory when the NSS flag is not given', () => {
		expect(adminNavFor(true).some((item) => item.href === '/admin/nss')).toBe(false);
	});

	it('should keep the Directory as the current section on its own page only', () => {
		expect(isCurrent('/admin/directory', '/admin/nss/accounts')).toBe(false);
	});

	it('should mark the NSS directory current on an account page', () => {
		expect(isCurrent('/admin/nss', '/admin/nss/accounts/10001')).toBe(true);
	});

	it('should mark the NSS directory current on a group page', () => {
		expect(isCurrent('/admin/nss', '/admin/nss/groups/5000')).toBe(true);
	});
});

describe('adminNavFor and every NSS key', () => {
	for (const isAdmin of [true, false]) {
		const who = isAdmin ? 'an admin' : 'SOC or an auditor';
		it(`should offer every NSS key when the viewer is ${who} and NSS is on`, () => {
			expect(adminNavFor(isAdmin, true).some((item) => item.href === '/admin/nss/keys')).toBe(true);
		});

		it(`should leave out every NSS key when the viewer is ${who} and NSS is off`, () => {
			expect(adminNavFor(isAdmin, false).some((item) => item.href === '/admin/nss/keys')).toBe(
				false
			);
		});
	}
});

describe('currentHref', () => {
	const hrefs = adminNavFor(true, true).map((item) => item.href);
	const cases: { name: string; pathname: string; want: string | null }[] = [
		{ name: 'the NSS keys list', pathname: '/admin/nss/keys', want: '/admin/nss/keys' },
		{ name: 'a stored account', pathname: '/admin/nss/accounts/10001', want: '/admin/nss' },
		{ name: 'the users list', pathname: '/admin/users', want: '/admin/users' },
		{ name: 'a page outside the admin area', pathname: '/dashboard', want: null }
	];

	for (const { name, pathname, want } of cases) {
		it(`should mark ${want ?? 'nothing'} when the page is ${name}`, () => {
			expect(currentHref(hrefs, pathname)).toBe(want);
		});
	}
});

describe('nssKeysNav', () => {
	it('should point at the fleet key list outside the admin area', () => {
		expect(nssKeysNav().href).toBe('/nss-keys');
	});

	it('should keep the fleet key list out of the admin group', () => {
		expect(adminNav.some((item) => item.href === '/nss-keys')).toBe(false);
	});
});
