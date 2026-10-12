import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Test methodology: the audit page keeps its two filters (action and source
// address) in the address, as /logs/me does. The router is stubbed: a fake
// page for what the address says, and a goto that records what the page
// writes back. fetch answers by URL, and every URL asked for is kept,
// which is how a test reads the filters a request carried.
const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', () => ({ goto }));

import { fakeGoto, resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

/** shallowReplace is how a filter reaches the address: in place, without a
 * navigation and without a history entry of its own. */
const shallowReplace = expect.objectContaining({ shallow: true, replace: true });

/** auditEvent builds one feed row, from a source address when one is given. */
function auditEvent(id: string, action: string, sourceIP?: string) {
	return {
		id,
		created_at: '2026-10-12T09:00:00Z',
		action,
		system: true,
		detail: sourceIP ? { source_ip: sourceIP, cert_type: 'console' } : { cert_type: 'console' }
	};
}

const requested: URL[] = [];

/** stubFeed answers every read with the events answer(url) returns. */
function stubFeed(answer: (url: URL) => object[] = () => []) {
	requested.length = 0;
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = new URL(String(input), 'http://localhost');
			requested.push(url);
			const events = answer(url);
			return Promise.resolve(
				new Response(JSON.stringify({ data: { events, total: events.length }, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

/** lastRequest is the newest feed read. */
function lastRequest(): URL {
	return requested[requested.length - 1];
}

/** settle lets the page's first load run. */
async function settle() {
	await new Promise((resolve) => setTimeout(resolve, 0));
}

beforeEach(() => {
	goto.mockReset();
	// Shallow routing as SvelteKit does it: page.state is reassigned on
	// every write and page.url never moves. A page that tracked either
	// would write again on its own write (#178).
	goto.mockImplementation(fakeGoto);
	resetFakePage('http://localhost/admin/audit');
});

afterEach(() => {
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

describe('audit filters read from the address', () => {
	it('should request the action the address names', async () => {
		resetFakePage('http://localhost/admin/audit?action=cert.request_refused_network');
		stubFeed();
		render(Page);
		await settle();
		expect(lastRequest().searchParams.get('action')).toBe('cert.request_refused_network');
	});

	it('should request the source address the address names', async () => {
		resetFakePage('http://localhost/admin/audit?source_ip=192.0.2.7');
		stubFeed();
		render(Page);
		await settle();
		expect(lastRequest().searchParams.get('source_ip')).toBe('192.0.2.7');
	});

	it('should show the opened action in the select', async () => {
		resetFakePage('http://localhost/admin/audit?action=auth.login');
		stubFeed();
		render(Page);
		await settle();
		expect(screen.getByTestId('audit-action-filter')).toHaveValue('auth.login');
	});

	it('should drop an action the taxonomy does not know', async () => {
		resetFakePage('http://localhost/admin/audit?action=cert.nonsense');
		stubFeed();
		render(Page);
		await settle();
		expect(lastRequest().searchParams.has('action')).toBe(false);
	});

	it('should not request an opened address that does not parse', async () => {
		resetFakePage('http://localhost/admin/audit?source_ip=not-an-ip');
		stubFeed();
		render(Page);
		await settle();
		expect(lastRequest().searchParams.has('source_ip')).toBe(false);
	});

	it('should mark an opened address that does not parse', async () => {
		resetFakePage('http://localhost/admin/audit?source_ip=not-an-ip');
		stubFeed();
		render(Page);
		await settle();
		expect(screen.getByTestId('audit-source-filter')).toHaveAttribute('aria-invalid', 'true');
	});
});

describe('audit filters changed on the page', () => {
	it('should request the action chosen in the select', async () => {
		stubFeed();
		render(Page);
		await settle();
		await userEvent.selectOptions(screen.getByTestId('audit-action-filter'), 'cert.requested');
		await waitFor(() => expect(lastRequest().searchParams.get('action')).toBe('cert.requested'));
	});

	it('should write a chosen action back to the address', async () => {
		stubFeed();
		render(Page);
		await settle();
		await userEvent.selectOptions(screen.getByTestId('audit-action-filter'), 'cert.requested');
		await waitFor(() =>
			expect(goto).toHaveBeenCalledWith('/admin/audit?action=cert.requested', shallowReplace)
		);
	});

	// Issue #178: the write used to re-run the effect that made it, which
	// rewrote the address once per microtask and hung the page.
	it('should write the address once when the action changes', async () => {
		stubFeed();
		render(Page);
		await settle();
		await userEvent.selectOptions(screen.getByTestId('audit-action-filter'), 'cert.requested');
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(goto).toHaveBeenCalledTimes(1);
	});

	it('should request a typed address once Enter settles it', async () => {
		stubFeed();
		render(Page);
		await settle();
		await userEvent.type(screen.getByTestId('audit-source-filter'), '2001:db8::1{Enter}');
		await waitFor(() => expect(lastRequest().searchParams.get('source_ip')).toBe('2001:db8::1'));
	});

	it('should request a typed address once typing settles', async () => {
		stubFeed();
		render(Page);
		await settle();
		await userEvent.type(screen.getByTestId('audit-source-filter'), '192.0.2.7');
		await waitFor(() => expect(lastRequest().searchParams.get('source_ip')).toBe('192.0.2.7'));
	});

	it('should say so when Enter settles an address that does not parse', async () => {
		stubFeed();
		render(Page);
		await settle();
		await userEvent.type(screen.getByTestId('audit-source-filter'), '192.0.2{Enter}');
		expect(await screen.findByTestId('audit-source-error')).toHaveTextContent(
			'Enter one IP address'
		);
	});

	it('should not request an address that does not parse', async () => {
		stubFeed();
		render(Page);
		await settle();
		await userEvent.type(screen.getByTestId('audit-source-filter'), '192.0.2{Enter}');
		await screen.findByTestId('audit-source-error');
		expect(requested.some((url) => url.searchParams.has('source_ip'))).toBe(false);
	});

	it('should write both filters back to the address', async () => {
		stubFeed();
		render(Page);
		await settle();
		await userEvent.selectOptions(
			screen.getByTestId('audit-action-filter'),
			'cert.request_refused_network'
		);
		await userEvent.type(screen.getByTestId('audit-source-filter'), '192.0.2.7{Enter}');
		await waitFor(() =>
			expect(goto).toHaveBeenCalledWith(
				'/admin/audit?action=cert.request_refused_network&source_ip=192.0.2.7',
				shallowReplace
			)
		);
	});
});

describe('a source address clicked in a row', () => {
	it('should link to the feed filtered to that address with the action kept', async () => {
		resetFakePage('http://localhost/admin/audit?action=cert.request_refused_network');
		stubFeed(() => [auditEvent('e1', 'cert.request_refused_network', '203.0.113.9')]);
		render(Page);
		expect(await screen.findByTestId('audit-source-link')).toHaveAttribute(
			'href',
			'/admin/audit?action=cert.request_refused_network&source_ip=203.0.113.9'
		);
	});

	it('should filter the feed to that address in place', async () => {
		stubFeed(() => [auditEvent('e1', 'cert.request_refused_network', '203.0.113.9')]);
		render(Page);
		await userEvent.click(await screen.findByTestId('audit-source-link'));
		await waitFor(() => expect(lastRequest().searchParams.get('source_ip')).toBe('203.0.113.9'));
	});

	it('should put the clicked address in the field', async () => {
		stubFeed(() => [auditEvent('e1', 'cert.request_refused_network', '203.0.113.9')]);
		render(Page);
		await userEvent.click(await screen.findByTestId('audit-source-link'));
		expect(screen.getByTestId('audit-source-filter')).toHaveValue('203.0.113.9');
	});
});

describe('audit filter states', () => {
	it('should say the filters match nothing when a filtered read is empty', async () => {
		resetFakePage('http://localhost/admin/audit?source_ip=192.0.2.7');
		stubFeed();
		render(Page);
		expect(await screen.findByTestId('audit-no-match')).toHaveTextContent(
			'No events match these filters'
		);
	});

	it('should say nothing has happened yet when the unfiltered feed is empty', async () => {
		stubFeed();
		render(Page);
		expect(await screen.findByTestId('audit-empty')).toHaveTextContent('No audit events yet');
	});

	it('should read the whole feed again when the no-match state is cleared', async () => {
		resetFakePage('http://localhost/admin/audit?action=auth.login&source_ip=192.0.2.7');
		stubFeed();
		render(Page);
		const empty = await screen.findByTestId('audit-no-match');
		await userEvent.click(within(empty).getByTestId('audit-no-match-clear'));
		await waitFor(() => expect(lastRequest().search).toBe('?limit=50'));
	});

	it('should keep the filters on screen when a filtered read fails', async () => {
		resetFakePage('http://localhost/admin/audit?action=auth.login');
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('offline')))
		);
		render(Page);
		await screen.findByTestId('audit-error');
		expect(screen.getByTestId('audit-clear-filters')).toBeInTheDocument();
	});

	it('should keep only the newest filter answer when an older read arrives late', async () => {
		let releaseFirst: () => void = () => {};
		vi.stubGlobal(
			'fetch',
			vi.fn((input: RequestInfo | URL) => {
				const url = new URL(String(input), 'http://localhost');
				const body = (events: object[]) =>
					new Response(JSON.stringify({ data: { events, total: events.length }, error: null }), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					});
				if (!url.searchParams.has('action')) {
					// The unfiltered first read hangs until released.
					return new Promise<Response>((resolve) => {
						releaseFirst = () => resolve(body([auditEvent('old', 'auth.login')]));
					});
				}
				return Promise.resolve(body([auditEvent('new', 'cert.requested', '192.0.2.7')]));
			})
		);
		render(Page);
		await settle();
		await userEvent.selectOptions(screen.getByTestId('audit-action-filter'), 'cert.requested');
		await screen.findByTestId('audit-source-link');
		releaseFirst();
		await settle();
		expect(screen.getByTestId('audit-event')).toHaveTextContent('cert.requested');
	});
});

describe('the action filter options', () => {
	it('should offer the network refusal', async () => {
		stubFeed();
		render(Page);
		await settle();
		const select = screen.getByTestId('audit-action-filter');
		expect(
			within(select).getByRole('option', { name: 'cert.request_refused_network' })
		).toBeInTheDocument();
	});

	it.each(['cert.issued', 'admin.audit_viewed', 'admin.user_viewed'])(
		'should not offer %s, which the feed never returns',
		async (action) => {
			stubFeed();
			render(Page);
			await settle();
			const select = screen.getByTestId('audit-action-filter');
			expect(within(select).queryByRole('option', { name: action })).not.toBeInTheDocument();
		}
	);
});
