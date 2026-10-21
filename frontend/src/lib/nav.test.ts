import { describe, expect, it } from 'vitest';

import {
	currentHref,
	adminNav,
	adminNavFor,
	adminRouteOpen,
	isAdminRoute,
	isCurrent,
	isFocusRoute,
	nssKeysNav,
	primaryNav,
	accountNav,
	type AdminViewer
} from './nav';

// Test methodology: table-driven over the path predicates, which are the
// only part of the model that can be wrong at runtime. The boundary cases
// are the point — a bare prefix match would light up the wrong rail item
// and would strip the rail from /console, and both bugs are invisible until
// somebody navigates there.

/** Role names a viewer by what it holds; admin implies the rest. */
type Role = 'admin' | 'SOC or an auditor' | 'a fleet key admin' | 'a plain user';

/** viewer is the session's roles for role, with NSS on or off. */
function viewer(role: Role, nssEnabled = true): AdminViewer {
	return {
		is_admin: role === 'admin',
		is_auditor: role === 'admin' || role === 'SOC or an auditor',
		is_nss_key_admin: role === 'admin' || role === 'a fleet key admin',
		nss_enabled: nssEnabled
	};
}

describe('isFocusRoute', () => {
	const cases: Array<{ name: string; pathname: string; want: boolean }> = [
		{ name: 'the sign-in page', pathname: '/login', want: true },
		{ name: 'a console code', pathname: '/c/ABCD-1234', want: true },
		{ name: 'the approval-unavailable notice', pathname: '/approval-unavailable', want: true },
		{ name: 'the bootstrap claims echo', pathname: '/identity/echo', want: true },
		{ name: 'the admin claims echo', pathname: '/admin/identity/echo', want: false },
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
			'NSS keys (all)',
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
		expect(adminNavFor(viewer('admin'))).toEqual(adminNav);
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
			expect(adminNavFor(viewer('SOC or an auditor')).map((item) => item.href)).not.toContain(href);
		});

		it(`should offer ${href} when the viewer is an admin`, () => {
			expect(adminNavFor(viewer('admin')).map((item) => item.href)).toContain(href);
		});
	}

	it('should keep every section not marked admin-only when the viewer is not an admin', () => {
		expect(adminNavFor(viewer('SOC or an auditor'))).toEqual(
			adminNav.filter((item) => !item.adminOnly)
		);
	});

	it('should mark only Config, the Directory, Claims echo and Diagnostics as admin-only', () => {
		expect(adminNav.filter((item) => item.adminOnly).map((item) => item.href)).toEqual(
			adminOnlyHrefs
		);
	});

	// The fleet key admin role manages every key, so it opens that list,
	// and nothing else of the admin area.
	it('should offer only every NSS key when the viewer is a fleet key admin with no other role', () => {
		expect(adminNavFor(viewer('a fleet key admin')).map((item) => item.href)).toEqual([
			'/admin/nss/keys'
		]);
	});

	it('should offer nothing when the viewer is a fleet key admin and NSS is off', () => {
		expect(adminNavFor(viewer('a fleet key admin', false))).toEqual([]);
	});

	it('should offer nothing when the viewer holds no role', () => {
		expect(adminNavFor(viewer('a plain user'))).toEqual([]);
	});

	it('should offer nothing when there is no viewer', () => {
		expect(adminNavFor(null)).toEqual([]);
	});

	it('should mark only every NSS key as open to the fleet key admin role', () => {
		expect(adminNav.filter((item) => item.keyAdmin).map((item) => item.href)).toEqual([
			'/admin/nss/keys'
		]);
	});
});

// The NSS directory viewer is read at auditor level, so every admin-area
// role is offered it, but only where the server serves NSS.
describe('adminNavFor and the NSS directory', () => {
	const cases: { role: Role; nssEnabled: boolean; want: boolean }[] = [
		{ role: 'admin', nssEnabled: true, want: true },
		{ role: 'SOC or an auditor', nssEnabled: true, want: true },
		{ role: 'a fleet key admin', nssEnabled: true, want: false },
		{ role: 'admin', nssEnabled: false, want: false },
		{ role: 'SOC or an auditor', nssEnabled: false, want: false }
	];

	for (const { role, nssEnabled, want } of cases) {
		it(`should ${want ? 'offer' : 'leave out'} the NSS directory when the viewer is ${role} with NSS ${nssEnabled ? 'on' : 'off'}`, () => {
			expect(adminNavFor(viewer(role, nssEnabled)).some((item) => item.href === '/admin/nss')).toBe(
				want
			);
		});
	}

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
	const roles: Role[] = ['admin', 'SOC or an auditor', 'a fleet key admin'];
	for (const role of roles) {
		it(`should offer every NSS key when the viewer is ${role} and NSS is on`, () => {
			expect(adminNavFor(viewer(role)).some((item) => item.href === '/admin/nss/keys')).toBe(true);
		});

		it(`should leave out every NSS key when the viewer is ${role} and NSS is off`, () => {
			expect(adminNavFor(viewer(role, false)).some((item) => item.href === '/admin/nss/keys')).toBe(
				false
			);
		});
	}

	it('should leave out every NSS key when the viewer holds no role', () => {
		expect(
			adminNavFor(viewer('a plain user')).some((item) => item.href === '/admin/nss/keys')
		).toBe(false);
	});
});

// The admin layout's gate. Every admin page for the admin area's roles;
// All NSS keys alone for the fleet key admin role; nothing for anyone else.
describe('adminRouteOpen', () => {
	const cases: { role: Role | null; pathname: string; want: boolean }[] = [
		{ role: 'admin', pathname: '/admin/users', want: true },
		{ role: 'SOC or an auditor', pathname: '/admin/users', want: true },
		{ role: 'SOC or an auditor', pathname: '/admin/nss/keys', want: true },
		{ role: 'a fleet key admin', pathname: '/admin/nss/keys', want: true },
		{ role: 'a fleet key admin', pathname: '/admin/users', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/users/u-1', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/certificates', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/service-codes', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/audit', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/config', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/directory', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/diagnostics', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/identity/echo', want: false },
		// The NSS directory is the parent of the keys' path, not under it.
		{ role: 'a fleet key admin', pathname: '/admin/nss', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/nss/accounts', want: false },
		{ role: 'a fleet key admin', pathname: '/admin/nss/keysmith', want: false },
		{ role: 'a plain user', pathname: '/admin/nss/keys', want: false },
		{ role: null, pathname: '/admin/nss/keys', want: false }
	];

	for (const { role, pathname, want } of cases) {
		it(`should ${want ? 'open' : 'refuse'} ${pathname} when the viewer is ${role ?? 'nobody'}`, () => {
			expect(adminRouteOpen(role ? viewer(role) : null, pathname)).toBe(want);
		});
	}

	// NSS off does not close the page: it says so itself.
	it('should open every NSS key to a fleet key admin when NSS is off', () => {
		expect(adminRouteOpen(viewer('a fleet key admin', false), '/admin/nss/keys')).toBe(true);
	});
});

describe('currentHref', () => {
	const hrefs = adminNavFor(viewer('admin')).map((item) => item.href);
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
