import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { envelope, reportFixture, reportRow, routeFetch, userFixture } from '#lib/testing/nss.js';

// Test methodology: route the report to fixture rows and read the page the
// way an operator does after a rotation: the headline count, which hosts
// sit in which group, and the CSV for the rest of the team.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
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
	session.clear();
});

/** serve answers the report with the given body. */
function serve(response: Response) {
	routeFetch({ 'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': response });
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

	it('should say the key is not shared when the server answers 404', async () => {
		serve(envelope(null, 404, 'not found'));
		render(Page);

		expect(await screen.findByTestId('rotation-missing')).toBeInTheDocument();
	});
});
