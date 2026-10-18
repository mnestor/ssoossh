import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { userFixture } from '#lib/testing/nss.js';
import NSSDirectoryRef from './NSSDirectoryRef.svelte';

// Test methodology: the one sentence the NSS viewer uses to point at the
// admin-only Directory page, rendered for each section and each role. An
// admin gets a link to the section; SOC and auditors get the same sentence
// naming who can act, with nothing to follow into an access-denied card.

afterEach(() => session.clear());

const sections = [
	{ section: 'probe', anchor: 'probe', label: 'Probe' },
	{ section: 'sync', anchor: 'sync', label: 'Sync section' },
	{ section: 'pending', anchor: 'pending-id-changes', label: 'Pending id changes panel' },
	{ section: 'collisions', anchor: 'collisions', label: 'Collisions panel' }
] as const;

for (const { section, anchor, label } of sections) {
	describe(`reference to the ${label}`, () => {
		it('should link an admin to the section when the viewer is an admin', () => {
			session.user = userFixture({ is_admin: true, is_soc: true, is_auditor: true });
			render(NSSDirectoryRef, { section, testid: 'ref' });

			expect(screen.getByTestId('ref-link')).toHaveAttribute('href', `/admin/directory#${anchor}`);
		});

		it('should offer no link when the viewer is SOC', () => {
			session.user = userFixture({ is_soc: true, is_auditor: true });
			render(NSSDirectoryRef, { section, testid: 'ref' });

			expect(screen.queryByRole('link')).toBeNull();
		});

		it('should say an admin can act when the viewer is an auditor', () => {
			session.user = userFixture({ is_auditor: true });
			render(NSSDirectoryRef, { section, testid: 'ref' });

			expect(screen.getByTestId('ref')).toHaveTextContent(`An admin can`);
		});

		it('should name the section when the viewer is an auditor', () => {
			session.user = userFixture({ is_auditor: true });
			render(NSSDirectoryRef, { section, testid: 'ref' });

			expect(screen.getByTestId('ref')).toHaveTextContent(`the ${label} on the Directory page.`);
		});
	});
}
