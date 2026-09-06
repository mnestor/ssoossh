import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';

import { formatDateTime } from '$lib/format';
import AuditTimeline from './AuditTimeline.svelte';
import type { AuditEvent } from '$lib/api/types';

/** event builds one timeline row with sensible defaults. */
function event(overrides: Partial<AuditEvent> = {}): AuditEvent {
	return {
		id: 'evt-1',
		created_at: '2026-08-29T10:00:00Z',
		action: 'user.disabled',
		...overrides
	} as AuditEvent;
}

describe('AuditTimeline', () => {
	it('should say nothing was recorded when there are no events', () => {
		render(AuditTimeline, { props: { events: [] } });
		expect(screen.getByText('No audit events recorded.')).toBeInTheDocument();
	});

	// One row per user record an auditor opens, and one per page of the feed
	// they read, buries the decisions this view exists to show. The server
	// no longer writes either to the table the UI reads; rows recorded
	// before that stay until the retention sweep, so the display drops them
	// too.
	it('should not render a user-record view', () => {
		render(AuditTimeline, { props: { events: [event({ action: 'admin.user_viewed' })] } });
		expect(screen.getByText('No audit events recorded.')).toBeInTheDocument();
	});

	it('should not render a read of the audit log itself', () => {
		render(AuditTimeline, { props: { events: [event({ action: 'admin.audit_viewed' })] } });
		expect(screen.getByText('No audit events recorded.')).toBeInTheDocument();
	});

	it('should keep the decisions beside a dropped view event', () => {
		render(AuditTimeline, {
			props: {
				events: [
					event({ id: 'evt-1', action: 'admin.user_viewed' }),
					event({ id: 'evt-2', action: 'user.disabled' })
				]
			}
		});
		expect(screen.getAllByTestId('audit-event')).toHaveLength(1);
	});

	// An enrollment view is not in the hidden set: it is one event per code
	// opened, not one per screen, and the code is a credential.
	it('should still render an enrollment view', () => {
		render(AuditTimeline, { props: { events: [event({ action: 'admin.enrollment_viewed' })] } });
		expect(screen.getAllByTestId('audit-event')).toHaveLength(1);
	});

	it('should render an unknown action rather than dropping it', () => {
		// The taxonomy grows without a wire change, so a client that only
		// rendered known actions would silently hide new ones.
		render(AuditTimeline, { props: { events: [event({ action: 'future.thing' })] } });
		expect(screen.getAllByText('future.thing').length).toBeGreaterThan(0);
	});

	it('should name the actor when one is recorded', () => {
		render(AuditTimeline, {
			props: {
				events: [event({ actor: { user_id: 'u-bob', username: 'soc-bob' } })]
			}
		});
		expect(screen.getByText('soc-bob')).toBeInTheDocument();
	});

	it('should attribute a system action to the server', () => {
		// "Nobody" and "not recorded" have to read differently.
		render(AuditTimeline, {
			props: { events: [event({ action: 'enrollment.expired', system: true })] }
		});
		expect(screen.getByText('The server')).toBeInTheDocument();
	});

	it('should show the reason when the action carries one', () => {
		render(AuditTimeline, {
			props: { events: [event({ reason: 'offboarded, SEC-1234' })] }
		});
		expect(screen.getByTestId('audit-reason')).toHaveTextContent('offboarded, SEC-1234');
	});

	it('should call the page subject "this account" when it is the actor', () => {
		render(AuditTimeline, {
			props: {
				events: [event({ actor: { user_id: 'u-alice', username: 'alice' } })],
				subjectUserId: 'u-alice'
			}
		});
		expect(screen.getByText('This account')).toBeInTheDocument();
	});

	it('should not name the target when it is the same account as the actor', () => {
		// Otherwise a login row reads "alice signed in alice".
		render(AuditTimeline, {
			props: {
				events: [
					event({
						action: 'auth.login',
						actor: { user_id: 'u-alice', username: 'alice' },
						target: { user_id: 'u-alice', username: 'alice' }
					})
				]
			}
		});
		expect(screen.getAllByText('alice')).toHaveLength(1);
	});

	it('should render detail entries as key/value pairs', () => {
		render(AuditTimeline, {
			props: { events: [event({ detail: { serial: 42, principals: ['alice', 'a.smith'] } })] }
		});
		// The name is a column of its own, so it carries no trailing colon.
		expect(screen.getByText('serial')).toBeInTheDocument();
		expect(screen.getByText('42')).toBeInTheDocument();
		// A list detail is joined rather than rendered as "[object Object]".
		expect(screen.getByText('alice, a.smith')).toBeInTheDocument();
	});

	it('should render the timestamp with its timezone named', () => {
		render(AuditTimeline, {
			props: { events: [event({ created_at: '2026-09-04T18:24:27Z' })] }
		});

		const stamp = screen.getByText(formatDateTime('2026-09-04T18:24:27Z'));
		expect(stamp).toHaveAttribute('datetime', '2026-09-04T18:24:27Z');
	});

	it('should render nothing rather than crashing on an unexpected shape', () => {
		// The timeline is embedded in a page whose other content must survive
		// a malformed audit response.
		render(AuditTimeline, {
			props: { events: undefined as unknown as AuditEvent[] }
		});
		expect(screen.getByText('No audit events recorded.')).toBeInTheDocument();
	});
});
