import { readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

import { configDocsURL, configReferencePages } from './configdocs';

const referenceDir = join(__dirname, '../../../user-docs/src/content/docs/reference/config');

/** pagesOnSite lists the documented prefixes the way configdocs does. */
function pagesOnSite(dir: string): string[] {
	const out: string[] = [];
	for (const entry of readdirSync(dir)) {
		const full = join(dir, entry);
		if (statSync(full).isDirectory()) {
			out.push(...pagesOnSite(full));
			continue;
		}
		if (!entry.endsWith('.md')) continue;
		const rel = relative(referenceDir, full).replace(/\.md$/, '');
		if (rel === 'index' || rel === 'top-level') continue;
		out.push(
			rel
				.replace(/\/index$/, '')
				.split('/')
				.join('.')
		);
	}
	return out;
}

describe('configReferencePages', () => {
	// The site is generated from the config structs; a new struct page that
	// this list misses would send its keys to the wrong page.
	it('should list exactly the reference pages the documentation site has', () => {
		expect([...configReferencePages].sort()).toEqual(pagesOnSite(referenceDir).sort());
	});
});

describe('configDocsURL', () => {
	it.each([
		{ key: 'admin.require_group', want: '/admin/#require_group' },
		{ key: 'http.cookie_key', want: '/http/#cookie_key' },
		{ key: 'http.tls.min_version', want: '/http/tls/#min_version' },
		{
			key: 'cert_options.user.lifetime_policy.default_duration',
			want: '/cert_options/user/#lifetime_policydefault_duration'
		},
		{ key: 'fips', want: '/top-level/#fips' }
	])('should send $key to $want', ({ key, want }) => {
		expect(configDocsURL(key)).toBe(`https://mnestor.github.io/ssoossh/reference/config${want}`);
	});
});
