import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';

import type { DeniedRequest } from '$lib/api/types';
import DeniedRow from './DeniedRow.svelte';

// Test methodology: render the row against a fixed clock. What matters is
// what a denial has that a certificate row's fields cannot express -- the
// reported "user@host" it was refused for, a type that may not be knowable,
// and no destination to open, because a denial issues nothing.

const now = new Date('2026-08-02T12:00:00Z');

/** aDenial is one refusal, overridable per case. */
function aDenial(overrides: Partial<DeniedRequest> = {}): DeniedRequest {
	return {
		id: 'dec-1',
		certificate_request_id: 'req-1',
		type: 'pam',
		decided_at: '2026-08-02T10:00:00Z',
		reported_username: 'deploy',
		reported_hostname: 'rack07',
		pam_service: 'sudo',
		tty: 'pts/3',
		remote_host: '10.1.2.9',
		...overrides
	};
}

describe('DeniedRow', () => {
	it('should lead with the user and host the request reported', () => {
		render(DeniedRow, { denial: aDenial(), now, testid: 'row' });
		expect(screen.getByText('deploy@rack07')).toBeInTheDocument();
	});

	// "alice@" reads as a truncated address rather than as a missing
	// hostname, so a half-reported pair is shown as the half that exists.
	it('should show the username alone when no hostname was reported', () => {
		render(DeniedRow, { denial: aDenial({ reported_hostname: undefined }), now });
		expect(screen.getByText('deploy')).toBeInTheDocument();
	});

	it('should show the hostname alone when no username was reported', () => {
		render(DeniedRow, { denial: aDenial({ reported_username: undefined }), now });
		expect(screen.getByText('rack07')).toBeInTheDocument();
	});

	// The request id is the only thing every denial has, and it is what the
	// audit event for the refusal carries.
	it('should fall back to the request id when nothing was reported', () => {
		render(DeniedRow, {
			denial: aDenial({ reported_username: undefined, reported_hostname: undefined }),
			now
		});
		expect(screen.getByText('req-1')).toBeInTheDocument();
	});

	it('should name what was asked for', () => {
		render(DeniedRow, { denial: aDenial({ type: 'console' }), now, testid: 'row' });
		expect(screen.getByTestId('row')).toHaveTextContent('console request denied');
	});

	// The decisions table outlives certificate_requests by design. An
	// invented type would be a wrong answer where a blank is a missing one.
	it('should say only "request" when the type is not knowable', () => {
		render(DeniedRow, { denial: aDenial({ type: undefined }), now, testid: 'row' });
		expect(screen.getByTestId('row')).toHaveTextContent('request denied');
	});

	// The same mark a certificate row carries where the two are
	// interleaved, so one list reads as one list.
	it('should mark the row as denied', () => {
		render(DeniedRow, { denial: aDenial(), now, testid: 'row' });
		expect(screen.getByTestId('denial-outcome')).toHaveAttribute('data-outcome', 'denied');
	});

	it('should name the refusal for assistive technology', () => {
		render(DeniedRow, { denial: aDenial(), now, testid: 'row' });
		expect(screen.getByLabelText('Denied')).toBeInTheDocument();
	});

	// A denial issues nothing, so there is no certificate page to open. A
	// row that looked clickable and went nowhere would be worse than one
	// that plainly is not.
	it('should not be a link', () => {
		const { container } = render(DeniedRow, { denial: aDenial(), now, testid: 'row' });
		expect(container.querySelector('a')).toBeNull();
	});

	// The column a certificate row uses for the principals it granted. A
	// denial granted none, and what the request claimed it was doing is
	// what makes the refusal recognisable a month later.
	it('should say what the request claimed it was doing', () => {
		render(DeniedRow, { denial: aDenial(), now, testid: 'row' });
		expect(screen.getByTestId('row')).toHaveTextContent('sudo · pts/3 · from 10.1.2.9');
	});

	// A user request has no PAM service and no terminal -- there is no
	// session behind it -- so the reporting binary stands in.
	it('should fall back to the reporting client when there was no session', () => {
		render(DeniedRow, {
			denial: aDenial({
				type: 'user',
				pam_service: undefined,
				tty: undefined,
				remote_host: undefined,
				client: 'ssoossh/1.2.0'
			}),
			now,
			testid: 'row'
		});
		expect(screen.getByTestId('row')).toHaveTextContent('ssoossh/1.2.0');
	});

	// The client is the fallback, not an addition: a PAM request reports
	// both, and listing them together would push the session context out of
	// a column that truncates.
	it('should not name the client alongside the session context', () => {
		render(DeniedRow, { denial: aDenial({ client: 'ssoossh/1.2.0' }), now, testid: 'row' });
		expect(screen.getByTestId('row')).not.toHaveTextContent('ssoossh/1.2.0');
	});

	it('should say nothing about the claim when nothing was reported', () => {
		render(DeniedRow, {
			denial: aDenial({
				pam_service: undefined,
				tty: undefined,
				remote_host: undefined,
				client: undefined
			}),
			now,
			testid: 'row'
		});
		expect(screen.getByTestId('row')).not.toHaveTextContent('claimed:');
	});

	it('should say how long ago it was refused', () => {
		render(DeniedRow, { denial: aDenial(), now, testid: 'row' });
		expect(screen.getByTestId('row')).toHaveTextContent('2h ago');
	});
});
