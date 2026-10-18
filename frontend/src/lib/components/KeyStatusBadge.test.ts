import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';

import type { KeyDisplayStatus } from '#lib/nss.js';
import KeyStatusBadge from './KeyStatusBadge.svelte';

describe('KeyStatusBadge', () => {
	const statuses: KeyDisplayStatus[] = ['active', 'rotating', 'expired', 'revoked'];

	// Colour is never the only signal: every state says its name.
	for (const status of statuses) {
		it(`should name the state in words when the key is ${status}`, () => {
			render(KeyStatusBadge, { status, testid: 'badge' });

			expect(screen.getByTestId('badge')).toHaveTextContent(status);
		});
	}
});
