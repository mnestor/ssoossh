import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/svelte';

import { formatDateTime } from '#lib/format.js';
import AuditTimeline from './AuditTimeline.svelte';
import type { AuditEvent } from '#lib/api/types.js';

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

	// A refused self-disable names the same account as actor and target,
	// so the sentence is the actor and the phrase alone.
	it('should phrase a refused self-disable on the global feed', () => {
		const self = { user_id: 'u-alice', username: 'alice' };
		render(AuditTimeline, {
			props: { events: [event({ action: 'user.disable_refused', actor: self, target: self })] }
		});
		expect(screen.getByTestId('audit-event')).toHaveTextContent('alice was refused a self-disable');
	});

	it('should phrase a refused self-disable on the account timeline', () => {
		const self = { user_id: 'u-alice', username: 'alice' };
		render(AuditTimeline, {
			props: {
				events: [event({ action: 'user.disable_refused', actor: self, target: self })],
				subjectUserId: 'u-alice'
			}
		});
		expect(screen.getByTestId('audit-event')).toHaveTextContent(
			'This account was refused a self-disable'
		);
	});

	// Each row is shaped as the server records it: cert.claimed has no actor
	// (nobody has signed in yet), cert.expired, cert.sign_failed and
	// cert.request_refused_network are system events, and
	// admin.diagnostics_run names the admin who ran it. None of the first four
	// has an actor or a target, so they appear only on the global feed; the
	// per-user rows are what an account's own timeline shows.
	const admin = { user_id: 'u-alice', username: 'alice' };
	it.each([
		{
			view: 'the global feed',
			row: event({ action: 'cert.claimed' }),
			subjectUserId: '',
			want: 'Someone opened a certificate approval link'
		},
		{
			view: 'the global feed',
			row: event({ action: 'cert.expired', system: true }),
			subjectUserId: '',
			want: 'The server expired an unanswered certificate request'
		},
		{
			view: 'the global feed',
			row: event({ action: 'cert.sign_failed', system: true }),
			subjectUserId: '',
			want: 'The server could not sign an approved certificate'
		},
		{
			view: 'the global feed',
			row: event({ action: 'cert.request_refused_network', system: true }),
			subjectUserId: '',
			want: 'The server refused a certificate request from outside the allowed networks'
		},
		{
			view: 'the global feed',
			row: event({ action: 'admin.diagnostics_run', actor: admin }),
			subjectUserId: '',
			want: 'alice ran the deployment diagnostics'
		},
		{
			view: 'the account timeline',
			row: event({ action: 'admin.diagnostics_run', actor: admin }),
			subjectUserId: 'u-alice',
			want: 'This account ran the deployment diagnostics'
		},
		{
			view: 'another account timeline',
			row: event({ action: 'admin.diagnostics_run', actor: admin }),
			subjectUserId: 'u-bob',
			want: 'alice ran the deployment diagnostics'
		}
	])('should phrase $row.action on $view', ({ row, subjectUserId, want }) => {
		render(AuditTimeline, { props: { events: [row], subjectUserId } });
		expect(screen.getByTestId('audit-event').querySelector('p')).toHaveTextContent(want);
	});

	// A network refusal has no actor: the account it names is only what the
	// caller claimed, and has to read as a field, not as who did it.
	it.each([
		{ key: 'source_ip', value: '203.0.113.9' },
		{ key: 'claimed_username', value: 'root' },
		{ key: 'cert_type', value: 'console' }
	])('should show $key on a network refusal', ({ key, value }) => {
		render(AuditTimeline, {
			props: {
				events: [
					event({
						action: 'cert.request_refused_network',
						system: true,
						reason: 'the source address is outside cert_options.console.allowed_networks',
						detail: { cert_type: 'console', source_ip: '203.0.113.9', claimed_username: 'root' }
					})
				]
			}
		});
		const term = screen.getByText(key);
		expect(term.nextElementSibling).toHaveTextContent(value);
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

describe('AuditTimeline source links', () => {
	const refusal = event({
		action: 'cert.request_refused_network',
		system: true,
		detail: { source_ip: '203.0.113.9', approver_ip: '198.51.100.7' }
	});
	const href = (ip: string) => `/admin/audit?source_ip=${ip}`;

	it('should link a source address when the feed can filter on it', () => {
		render(AuditTimeline, { props: { events: [refusal], sourceHref: href } });
		expect(screen.getByTestId('audit-source-link')).toHaveAttribute(
			'href',
			'/admin/audit?source_ip=203.0.113.9'
		);
	});

	it('should name what the link does for a screen reader', () => {
		render(AuditTimeline, { props: { events: [refusal], sourceHref: href } });
		expect(screen.getByTestId('audit-source-link')).toHaveAccessibleName(
			'203.0.113.9, show only events from this address'
		);
	});

	it('should link only the source address and not the approver address', () => {
		render(AuditTimeline, { props: { events: [refusal], sourceHref: href } });
		expect(screen.getAllByTestId('audit-source-link')).toHaveLength(1);
	});

	it('should leave a source address as text when the timeline cannot filter', () => {
		render(AuditTimeline, { props: { events: [refusal] } });
		expect(screen.queryByTestId('audit-source-link')).not.toBeInTheDocument();
	});

	it('should apply the filter in place on a plain click', async () => {
		const onsource = vi.fn();
		render(AuditTimeline, { props: { events: [refusal], sourceHref: href, onsource } });
		await fireEvent.click(screen.getByTestId('audit-source-link'));
		expect(onsource).toHaveBeenCalledWith('203.0.113.9');
	});

	it('should leave a modified click to the browser', async () => {
		const onsource = vi.fn();
		render(AuditTimeline, { props: { events: [refusal], sourceHref: href, onsource } });
		await fireEvent.click(screen.getByTestId('audit-source-link'), { ctrlKey: true });
		expect(onsource).not.toHaveBeenCalled();
	});
});
