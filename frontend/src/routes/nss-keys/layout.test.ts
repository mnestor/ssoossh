import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { userFixture } from '#lib/testing/nss.js';
import { resetFakePage } from '#lib/testing/page.svelte.js';

// Test methodology: render the fleet key area's layout around a marker child
// in each session state. It is not a role gate (a key's group needs no
// role); it sends a signed-out visitor to sign in and says NSS is off
// rather than mounting pages whose every read would 404.

const { goToLogin } = vi.hoisted(() => ({ goToLogin: vi.fn() }));

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('#lib/auth.js', async () => {
	const actual = await vi.importActual<typeof import('#lib/auth.js')>('#lib/auth.js');
	return { ...actual, goToLogin };
});

import Layout from './+layout.svelte';

const children = createRawSnippet(() => ({
	render: () => '<p data-testid="nss-child">keys</p>'
}));

beforeEach(() => {
	resetFakePage('http://localhost/nss-keys');
	session.clear();
	goToLogin.mockReset();
});

afterEach(() => {
	session.clear();
});

describe('NSS keys layout', () => {
	it('should render the page for a signed-in user when NSS is on', () => {
		session.user = userFixture();
		render(Layout, { children });

		expect(screen.getByTestId('nss-child')).toBeInTheDocument();
	});

	it('should say NSS is off instead of the page when nss.enabled is false', () => {
		session.user = userFixture({ nss_enabled: false });
		render(Layout, { children });

		expect(screen.getByTestId('nss-keys-disabled')).toBeInTheDocument();
	});

	it('should not mount the page when nss.enabled is false', () => {
		session.user = userFixture({ nss_enabled: false });
		render(Layout, { children });

		expect(screen.queryByTestId('nss-child')).not.toBeInTheDocument();
	});

	it('should send a signed-out visitor to sign in with the page as the return path', () => {
		session.resolved = true;
		render(Layout, { children });

		expect(goToLogin).toHaveBeenCalledWith('/nss-keys');
	});

	it('should not mount the page before the session is known', () => {
		session.resolved = false;
		render(Layout, { children });

		expect(screen.queryByTestId('nss-child')).not.toBeInTheDocument();
	});
});
