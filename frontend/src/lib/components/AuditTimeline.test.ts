import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';

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
		expect(screen.getByText('serial:')).toBeInTheDocument();
		expect(screen.getByText('42')).toBeInTheDocument();
		// A list detail is joined rather than rendered as "[object Object]".
		expect(screen.getByText('alice, a.smith')).toBeInTheDocument();
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
