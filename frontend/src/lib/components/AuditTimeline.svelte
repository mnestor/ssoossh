<script lang="ts">
	import type { AuditEvent } from '$lib/api/types';

	interface Props {
		events: AuditEvent[];
		/**
		 * subjectUserId, when given, is the account whose page this is. It
		 * decides how each row reads: an event this account performed is
		 * phrased in the first person, one performed on it names the actor.
		 * Omit it on the global feed, where neither side is "you".
		 */
		subjectUserId?: string;
	}

	const { events, subjectUserId = '' }: Props = $props();

	// Defensive: an unexpected response shape must render "nothing to show"
	// rather than tearing down the page this timeline is embedded in.
	const rows = $derived(Array.isArray(events) ? events : []);

	/**
	 * Human phrasing per action. Unknown actions fall back to the raw
	 * namespaced name rather than being hidden: the taxonomy grows without a
	 * wire change, so a client that only rendered known actions would
	 * silently drop new ones.
	 */
	const phrasing: Record<string, string> = {
		'auth.login': 'signed in',
		'auth.login_denied': 'was refused sign-in',
		'cert.requested': 'requested a certificate',
		'cert.approved': 'approved a certificate',
		'cert.denied': 'denied a certificate request',
		'cert.code_resolved': 'opened a console login by its code',
		'enrollment.code_created': 'created a service enrollment',
		'enrollment.redeemed': 'redeemed a service enrollment',
		'enrollment.expired': 'expired a service enrollment',
		'enrollment.notification_email_set': "changed a service enrollment's notification address",
		'enrollment.reassigned': 'reassigned a service enrollment',
		'user.disabled': 'disabled an account',
		'user.enabled': 're-enabled an account',
		'user.auto_disabled': 'was disabled automatically',
		'admin.user_viewed': 'viewed a user record',
		'admin.enrollment_viewed': 'viewed an enrollment',
		'admin.config_viewed': 'viewed the effective configuration',
		'admin.audit_viewed': 'viewed the audit log'
	};

	function describe(event: AuditEvent): string {
		return phrasing[event.action] ?? event.action;
	}

	/** who names the actor, or the system when the server acted alone. */
	function who(event: AuditEvent): string {
		if (event.system) return 'The server';
		if (!event.actor) return 'Someone';
		if (subjectUserId && event.actor.user_id === subjectUserId) return 'This account';
		return event.actor.username || event.actor.subject || 'Someone';
	}

	/**
	 * onWhom names the target when it is a different account from the actor
	 * and from the page's subject, so a row does not read "alice disabled
	 * alice" on alice's own page.
	 */
	function onWhom(event: AuditEvent): string {
		if (!event.target) return '';
		if (event.target.user_id && event.target.user_id === event.actor?.user_id) return '';
		if (subjectUserId && event.target.user_id === subjectUserId) return 'this account';
		return event.target.username || event.target.subject || '';
	}

	/** Detail entries worth showing inline, in a stable order. */
	function details(event: AuditEvent): [string, string][] {
		if (!event.detail) return [];
		return Object.entries(event.detail)
			.filter(([, v]) => v !== null && v !== undefined && v !== '')
			.map(([k, v]): [string, string] => [k, Array.isArray(v) ? v.join(', ') : String(v)]);
	}
</script>

{#if rows.length === 0}
	<p class="text-sm text-ink-muted">No audit events recorded.</p>
{:else}
	<ol class="space-y-3" data-testid="audit-timeline">
		{#each rows as event (event.id)}
			<li class="border-l-2 border-border-subtle pl-3" data-testid="audit-event">
				<div class="flex flex-wrap items-baseline gap-x-2">
					<span class="text-sm text-ink">
						<strong>{who(event)}</strong>
						{describe(event)}
						{#if onWhom(event)}
							<strong>{onWhom(event)}</strong>
						{/if}
					</span>
					<span class="font-mono text-xs text-ink-muted">{event.action}</span>
					<time class="ml-auto text-xs text-ink-muted" datetime={event.created_at}>
						{new Date(event.created_at).toLocaleString()}
					</time>
				</div>

				{#if event.reason}
					<p class="mt-1 text-sm" data-testid="audit-reason">
						<span class="font-semibold text-ink-muted">Reason:</span>
						{event.reason}
					</p>
				{/if}

				{#if details(event).length > 0}
					<dl class="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-muted">
						{#each details(event) as [key, value] (key)}
							<div class="flex gap-1">
								<dt class="font-mono">{key}:</dt>
								<dd class="font-mono break-all">{value}</dd>
							</div>
						{/each}
					</dl>
				{/if}
			</li>
		{/each}
	</ol>
{/if}
