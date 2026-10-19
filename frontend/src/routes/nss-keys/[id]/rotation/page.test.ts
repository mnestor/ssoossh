import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { formatDateTime } from '#lib/format.js';
import { session } from '#lib/session.svelte.js';
import {
	envelope,
	keyFixture,
	reportFixture,
	reportRow,
	routeFetch,
	settle,
	userFixture,
	type Route
} from '#lib/testing/nss.js';

// Test methodology: route the report to fixture rows and read the page the
// way an operator does after a rotation: the headline count, which hosts
// sit in which group, and the CSV for the rest of the team.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
// A 401 would send the browser to sign in; here it is only recorded.
const { redirectIfUnauthenticated } = vi.hoisted(() => ({
	redirectIfUnauthenticated: vi.fn(
		(error: unknown) => (error as { isUnauthenticated?: boolean })?.isUnauthenticated === true
	)
}));
vi.mock('#lib/auth.js', async () => {
	const actual = await vi.importActual<typeof import('#lib/auth.js')>('#lib/auth.js');
	return { ...actual, redirectIfUnauthenticated };
});

import { fakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

const rows = [
	reportRow('10.0.0.3', 'not_switched', { last_seen: '2026-10-17T03:00:00Z', lookups: 500 }),
	reportRow('10.0.0.1', 'not_switched', { last_seen: '2026-10-17T05:00:00Z', lookups: 50 }),
	reportRow('10.0.1.1', 'switched'),
	reportRow('10.0.1.2', 'switched'),
	reportRow('10.0.1.3', 'switched'),
	reportRow('10.0.2.1', 'gone')
];

beforeEach(() => {
	session.user = userFixture();
	session.resolved = true;
	fakePage.params = { id: 'nss_k7m4qp2x' };
});

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
	redirectIfUnauthenticated.mockClear();
	session.clear();
});

/** serve answers the report with the given body, and the key beside it. */
function serve(
	response: Route | Response,
	key: Route | Response = envelope(keyFixture({ name: 'web-prod' }))
) {
	return routeFetch({
		'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': response,
		'GET /api/nss/keys/nss_k7m4qp2x': key
	});
}

/** inTurn answers each call with the next response, repeating the last. */
function inTurn(...responses: Response[]): Route {
	let call = 0;
	return () => responses[Math.min(call++, responses.length - 1)].clone();
}

/** addressesIn reads the source addresses down one bucket's table. */
function addressesIn(bucket: string): string[] {
	return within(screen.getByTestId(`rotation-${bucket}`))
		.getAllByTestId('rotation-row')
		.map((row) => row.querySelector('td')?.textContent?.trim() ?? '');
}

describe('Rotation report', () => {
	it('should lead with how many hosts are still on the old secret', async () => {
		serve(envelope(reportFixture(rows)));
		render(Page);

		expect(await screen.findByTestId('rotation-headline')).toHaveTextContent(
			'2 of 6 hosts still on the old secret'
		);
	});

	const counts: { bucket: string; want: string }[] = [
		{ bucket: 'not_switched', want: '2' },
		{ bucket: 'switched', want: '3' },
		{ bucket: 'gone', want: '1' }
	];

	for (const { bucket, want } of counts) {
		it(`should count ${want} ${bucket} hosts from the report`, async () => {
			serve(envelope(reportFixture(rows)));
			render(Page);

			expect(await screen.findByTestId(`rotation-count-${bucket}`)).toHaveTextContent(want);
		});
	}

	it('should put each address in its own bucket, newest last seen first', async () => {
		serve(envelope(reportFixture(rows)));
		render(Page);
		await screen.findByTestId('rotation-summary');

		expect({
			not_switched: addressesIn('not_switched'),
			switched: addressesIn('switched').length,
			gone: addressesIn('gone')
		}).toEqual({ not_switched: ['10.0.0.1', '10.0.0.3'], switched: 3, gone: ['10.0.2.1'] });
	});

	it('should order by lookups when that sort is chosen', async () => {
		serve(envelope(reportFixture(rows)));
		render(Page);
		await userEvent.selectOptions(await screen.findByTestId('rotation-sort'), 'lookups');

		expect(addressesIn('not_switched')).toEqual(['10.0.0.3', '10.0.0.1']);
	});

	it('should narrow every bucket to the addresses typed in the filter', async () => {
		serve(envelope(reportFixture(rows)));
		render(Page);
		await userEvent.type(await screen.findByTestId('rotation-filter'), '10.0.0.3');

		expect(addressesIn('not_switched')).toEqual(['10.0.0.3']);
	});

	it('should name the grace days left in the header', async () => {
		serve(envelope(reportFixture(rows)));
		render(Page);

		expect(await screen.findByTestId('rotation-grace')).toHaveTextContent(
			'accepted for 22 more days'
		);
	});

	it('should say the previous secret has ended when no grace is left', async () => {
		serve(envelope(reportFixture(rows, { grace_days_left: 0, previous_expires_at: undefined })));
		render(Page);

		expect(await screen.findByTestId('rotation-grace')).toHaveTextContent('no longer accepted');
	});

	it('should link the CSV export for this key', async () => {
		serve(envelope(reportFixture(rows)));
		render(Page);

		expect(await screen.findByTestId('rotation-csv')).toHaveAttribute(
			'href',
			'/api/nss/keys/nss_k7m4qp2x/rotation-report?format=csv'
		);
	});

	it('should say there is no rotation yet when the key was never rotated', async () => {
		serve(
			envelope(
				reportFixture([], {
					rotated_at: undefined,
					previous_expires_at: undefined,
					current_generation: 1
				})
			)
		);
		render(Page);

		expect(await screen.findByTestId('rotation-empty')).toHaveTextContent('No rotation yet');
	});

	it('should say None in a bucket with no hosts', async () => {
		serve(envelope(reportFixture([reportRow('10.0.1.1', 'switched')])));
		render(Page);

		expect(await screen.findByTestId('rotation-gone-none')).toBeInTheDocument();
	});

	it('should offer Try again when the report fails to load', async () => {
		serve(envelope(null, 500, 'boom'));
		render(Page);

		expect(await screen.findByTestId('rotation-error-retry')).toBeInTheDocument();
	});

	describe('naming the key', () => {
		const cases: { name: string; key: Response; testid: string; want: string }[] = [
			{
				name: 'the key reads',
				key: envelope(keyFixture({ name: 'web-prod' })),
				testid: 'rotation-back',
				want: 'web-prod'
			},
			{
				name: 'the key read fails',
				key: envelope(null, 500, 'boom'),
				testid: 'rotation-back',
				want: 'Key nss_k7m4qp2x'
			},
			{
				name: 'the key reads, in the sub line',
				key: envelope(keyFixture({ name: 'web-prod' })),
				testid: 'rotation-key-name',
				want: 'web-prod'
			}
		];
		for (const { name, key, testid, want } of cases) {
			it(`should read ${want} in ${testid} when ${name}`, async () => {
				serve(envelope(reportFixture(rows)), key);
				render(Page);
				await screen.findByTestId('rotation-summary');
				await settle();

				expect(screen.getByTestId(testid)).toHaveTextContent(want);
			});
		}

		it('should name the key in the tab title when the key reads', async () => {
			serve(envelope(reportFixture(rows)));
			render(Page);
			await screen.findByTestId('rotation-summary');

			await waitFor(() => expect(document.title).toBe('Rotation report · web-prod · ssoossh'));
		});
	});

	const revokedCases: { name: string; status: 'revoked' | 'active'; shown: boolean }[] = [
		{ name: 'the key is revoked', status: 'revoked', shown: true },
		{ name: 'the key is live', status: 'active', shown: false }
	];
	for (const { name, status, shown } of revokedCases) {
		it(`should ${shown ? 'say' : 'not say'} the key is revoked when ${name}`, async () => {
			serve(envelope(reportFixture(rows)), envelope(keyFixture({ status })));
			render(Page);
			await screen.findByTestId('rotation-summary');

			expect(screen.queryByTestId('rotation-revoked') !== null).toBe(shown);
		});
	}

	describe('once the previous secret has ended', () => {
		const ended = { grace_days_left: 0, previous_expires_at: undefined };

		it('should say the hosts on the old secret are failing when the grace is over', async () => {
			serve(envelope(reportFixture(rows, ended)));
			render(Page);

			expect(await screen.findByTestId('rotation-headline')).toHaveTextContent(
				'2 of 6 hosts present a secret that is no longer accepted'
			);
		});

		const toneCases: { name: string; overrides: object; danger: boolean }[] = [
			{ name: 'the grace is over', overrides: ended, danger: true },
			{ name: 'the grace is still running', overrides: {}, danger: false }
		];
		for (const { name, overrides, danger } of toneCases) {
			it(`should ${danger ? 'mark' : 'not mark'} the headline in red when ${name}`, async () => {
				serve(envelope(reportFixture(rows, overrides)));
				render(Page);

				expect(
					(await screen.findByTestId('rotation-headline')).classList.contains('text-danger')
				).toBe(danger);
			});
		}

		it('should say the not switched hosts resolve no accounts when the grace is over', async () => {
			serve(envelope(reportFixture(rows, ended)));
			render(Page);

			expect(await screen.findByTestId('rotation-not_switched')).toHaveTextContent(
				'Last presented an older secret, which is no longer accepted.'
			);
		});
	});

	describe('refresh', () => {
		const later = reportFixture([
			reportRow('10.0.0.3', 'switched'),
			reportRow('10.0.0.1', 'not_switched'),
			reportRow('10.0.1.1', 'switched'),
			reportRow('10.0.1.2', 'switched'),
			reportRow('10.0.1.3', 'switched'),
			reportRow('10.0.2.1', 'gone')
		]);

		it('should show the newer count when Refresh is pressed', async () => {
			serve(inTurn(envelope(reportFixture(rows)), envelope(later)));
			render(Page);
			await userEvent.click(await screen.findByTestId('rotation-refresh'));

			await waitFor(() =>
				expect(screen.getByTestId('rotation-headline')).toHaveTextContent(
					'1 of 6 hosts still on the old secret'
				)
			);
		});

		it('should keep the address filter when Refresh is pressed', async () => {
			serve(inTurn(envelope(reportFixture(rows)), envelope(later)));
			render(Page);
			await userEvent.type(await screen.findByTestId('rotation-filter'), '10.0.0');
			await userEvent.click(screen.getByTestId('rotation-refresh'));
			await settle();

			expect(screen.getByTestId('rotation-filter')).toHaveValue('10.0.0');
		});

		it('should keep the report on screen while a refresh is in flight', async () => {
			serve(inTurn(envelope(reportFixture(rows)), envelope(later)));
			render(Page);
			await screen.findByTestId('rotation-summary');
			serve(() => new Promise<Response>(() => {}));
			await userEvent.click(screen.getByTestId('rotation-refresh'));

			expect(screen.getByTestId('rotation-summary')).toBeInTheDocument();
		});

		it('should keep the last figures and say so when a refresh fails', async () => {
			serve(inTurn(envelope(reportFixture(rows)), envelope(null, 500, 'database unavailable')));
			render(Page);
			await userEvent.click(await screen.findByTestId('rotation-refresh'));

			expect(await screen.findByTestId('rotation-refresh-error')).toHaveTextContent(
				'The figures below are from the last read.'
			);
		});

		it('should say when the report was read when it loads', async () => {
			vi.useFakeTimers({ now: new Date('2026-10-18T12:00:00Z'), toFake: ['Date'] });
			serve(envelope(reportFixture(rows)));
			render(Page);

			expect(await screen.findByTestId('rotation-read-at')).toHaveTextContent(
				`As of ${formatDateTime('2026-10-18T12:00:00.000Z')}.`
			);
			vi.useRealTimers();
		});

		it('should announce the newer count when a refresh lands', async () => {
			serve(inTurn(envelope(reportFixture(rows)), envelope(later)));
			render(Page);
			await userEvent.click(await screen.findByTestId('rotation-refresh'));

			await waitFor(() =>
				expect(screen.getByTestId('rotation-refresh-live')).toHaveTextContent(
					'Report refreshed. 1 of 6 hosts still on the old secret.'
				)
			);
		});
	});

	describe('CSV download', () => {
		/** pressCSV loads the report, then answers the check with `check` and presses the link. */
		async function pressCSV(check: Response) {
			serve(inTurn(envelope(reportFixture(rows)), check));
			const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
			render(Page);
			await userEvent.click(await screen.findByTestId('rotation-csv'));
			await settle();
			return click;
		}

		const cases: { name: string; check: Response; downloads: boolean }[] = [
			{ name: 'the session is live', check: envelope(reportFixture(rows)), downloads: true },
			{ name: 'the session has expired', check: envelope(null, 401, 'sign in'), downloads: false },
			{ name: 'the server fails', check: envelope(null, 500, 'boom'), downloads: false }
		];
		for (const { name, check, downloads } of cases) {
			it(`should ${downloads ? 'let' : 'stop'} the download when ${name}`, async () => {
				const click = await pressCSV(check);

				expect(click.mock.calls.length).toBe(downloads ? 1 : 0);
			});
		}

		it('should send the reader to sign in when the session has expired', async () => {
			await pressCSV(envelope(null, 401, 'sign in'));

			expect(redirectIfUnauthenticated).toHaveReturnedWith(true);
		});

		it('should say the download failed when the server fails', async () => {
			await pressCSV(envelope(null, 500, 'boom'));

			expect(screen.getByTestId('rotation-csv-error')).toHaveTextContent(
				'Press Download CSV to try again.'
			);
		});
	});

	it('should leave the address keyboard open to letters and colons for IPv6', async () => {
		serve(envelope(reportFixture(rows)));
		render(Page);

		expect(await screen.findByTestId('rotation-filter')).not.toHaveAttribute('inputmode');
	});

	it('should say the key is not shared when the server answers 404', async () => {
		serve(envelope(null, 404, 'not found'));
		render(Page);

		expect(await screen.findByTestId('rotation-missing')).toBeInTheDocument();
	});
});
