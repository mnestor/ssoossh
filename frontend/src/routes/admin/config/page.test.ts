import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { EffectiveConfigResponse } from '$lib/api/types';
import Page from './+page.svelte';

// Test methodology: render the effective-config view against a stubbed
// fetch. The page names no configuration key of its own — the server
// reflects over its config struct and the page renders what arrives — so
// these cases pin the rendering rules instead: sections and keys appear,
// unset keys are held back until asked for, a redacted key still reports
// whether it is set, and a load failure does not read as an empty config.

/** effectiveConfig builds an answer with one of each kind of setting. */
function effectiveConfig(): EffectiveConfigResponse {
	return {
		sections: [
			{
				name: 'server',
				settings: [
					{ key: 'production', value: 'true', secret: false },
					{ key: 'ssh_key', value: '[redacted]', secret: true },
					{ key: 'multi_instance', value: 'false', secret: false }
				]
			},
			{
				name: 'http',
				settings: [
					{ key: 'http.public_url', value: 'https://ssh.example.com:8443', secret: false },
					{ key: 'http.port', value: '8443', secret: false },
					{ key: 'http.cookie_key', value: '', secret: true },
					{ key: 'http.unix_socket', value: '', secret: false }
				]
			},
			{
				name: 'cert_options',
				settings: [
					{ key: 'cert_options.user.valid_duration', value: '8h0m0s', secret: false },
					{ key: 'cert_options.user.require', value: 'claim loc >= 40', secret: false }
				]
			}
		]
	};
}

/** stubConfig answers the admin config fetch with the given payload. */
function stubConfig(config: EffectiveConfigResponse) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: config, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('admin config page', () => {
	it('should render every section it is handed', async () => {
		stubConfig(effectiveConfig());

		render(Page);

		expect(await screen.findByText('server')).toBeInTheDocument();
		expect(screen.getByText('http')).toBeInTheDocument();
		expect(screen.getByText('cert_options')).toBeInTheDocument();
	});

	it('should render a key beside the value in effect', async () => {
		stubConfig(effectiveConfig());

		render(Page);

		expect(await screen.findByText('http.public_url')).toBeInTheDocument();
		expect(screen.getByText('https://ssh.example.com:8443')).toBeInTheDocument();
	});

	it('should render a nested certificate policy key in full', async () => {
		stubConfig(effectiveConfig());

		render(Page);

		expect(await screen.findByText('cert_options.user.require')).toBeInTheDocument();
		expect(screen.getByText('claim loc >= 40')).toBeInTheDocument();
	});

	it('should hold back unset keys until they are asked for', async () => {
		stubConfig(effectiveConfig());

		render(Page);

		expect(await screen.findByText('http.port')).toBeInTheDocument();
		expect(screen.queryByText('http.unix_socket')).not.toBeInTheDocument();
	});

	it('should reveal unset keys when the toggle is checked', async () => {
		stubConfig(effectiveConfig());

		render(Page);
		await screen.findByText('http.port');
		await userEvent.click(screen.getByLabelText('Show unset keys'));

		expect(screen.getByText('http.unix_socket')).toBeInTheDocument();
	});

	it('should count the keys that are set against the whole configuration', async () => {
		stubConfig(effectiveConfig());

		render(Page);

		expect(await screen.findByTestId('config-count')).toHaveTextContent('7 of 9 keys set');
	});

	it('should mark a redacted key as secret', async () => {
		stubConfig(effectiveConfig());

		render(Page);

		expect(await screen.findByText('ssh_key')).toBeInTheDocument();
		expect(screen.getAllByText('secret').length).toBeGreaterThan(0);
	});

	// A secret that is not configured is a different answer from one that
	// is, and the difference discloses nothing.
	it('should show an unset secret as not set rather than redacted', async () => {
		stubConfig(effectiveConfig());

		render(Page);
		await screen.findByText('ssh_key');
		await userEvent.click(screen.getByLabelText('Show unset keys'));

		const row = screen.getByText('http.cookie_key').parentElement;
		expect(row).toHaveTextContent('not set');
	});

	it('should narrow the view to the keys matching the filter', async () => {
		stubConfig(effectiveConfig());

		render(Page);
		await screen.findByText('http.port');
		await userEvent.type(screen.getByTestId('config-search'), 'cookie');

		// A typed filter reaches unset keys too: cookie_key has no value,
		// and "no match" would read as "no such key".
		await waitFor(() => expect(screen.queryByText('http.port')).not.toBeInTheDocument());
		expect(screen.getByText('http.cookie_key')).toBeInTheDocument();
	});

	it('should match a filter against a value as well as a key', async () => {
		stubConfig(effectiveConfig());

		render(Page);
		await screen.findByText('http.port');
		await userEvent.type(screen.getByTestId('config-search'), '8443');

		await waitFor(() => expect(screen.queryByText('production')).not.toBeInTheDocument());
		expect(screen.getByText('http.public_url')).toBeInTheDocument();
	});

	it('should say so when a filter matches nothing', async () => {
		stubConfig(effectiveConfig());

		render(Page);
		await screen.findByText('http.port');
		await userEvent.type(screen.getByTestId('config-search'), 'nothing-matches-this');

		await waitFor(() => expect(screen.getByTestId('config-empty')).toBeInTheDocument());
	});

	it('should surface a load failure instead of an empty view', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(JSON.stringify({ data: null, error: 'not authorized as auditor' }), {
						status: 403,
						headers: { 'Content-Type': 'application/json' }
					})
				)
			)
		);

		render(Page);

		expect(await screen.findByText('not authorized as auditor')).toBeInTheDocument();
	});
});
