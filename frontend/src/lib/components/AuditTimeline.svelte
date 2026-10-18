<script lang="ts">
	import type { AuditEvent } from '#lib/api/types.js';
	import { describeAuditAction, detailRows, visibleAuditEvents } from '#lib/audit.js';
	import { formatDateTime } from '#lib/format.js';

	interface Props {
		events: AuditEvent[];
		/**
		 * subjectUserId, when given, is the account whose page this is. It
		 * decides how each row reads: an event this account performed is
		 * phrased in the first person, one performed on it names the actor.
		 * Omit it on the global feed, where neither side is "you".
		 */
		subjectUserId?: string;
		/**
		 * sourceHref, when given, turns each event's source_ip into a link
		 * that filters the feed to that address: the address of the view
		 * filtered that way. The global feed passes it; a user's timeline
		 * does not, since it cannot filter.
		 */
		sourceHref?: (ip: string) => string;
		/**
		 * onsource, with sourceHref, applies the filter in place on a plain
		 * click instead of navigating, so the page keeps its state. A
		 * modified click (a new tab, say) still follows the href.
		 */
		onsource?: (ip: string) => void;
	}

	const { events, subjectUserId = '', sourceHref, onsource }: Props = $props();

	/**
	 * followSource applies the source filter in place on a plain left click.
	 * Anything else (a middle click, a modifier key) is left to the browser,
	 * which opens the href, so the link behaves like every other link.
	 */
	function followSource(event: MouseEvent, ip: string) {
		if (
			!onsource ||
			event.button !== 0 ||
			event.metaKey ||
			event.ctrlKey ||
			event.shiftKey ||
			event.altKey
		) {
			return;
		}
		event.preventDefault();
		onsource(ip);
	}

	// The hidden privileged-view actions and the array guard both live in
	// #lib/audit, so this and the page that counts what it rendered cannot
	// disagree about which rows are shown.
	const rows = $derived(visibleAuditEvents(events));

	/** describe is the row's verb phrase; see auditPhrasing in #lib/audit. */
	function describe(event: AuditEvent): string {
		return describeAuditAction(event.action);
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

	/** Detail entries worth showing inline, in a stable order; nested
	 * objects spread into dotted names (see detailRows). */
	function details(event: AuditEvent): [string, string][] {
		return detailRows(event.detail);
	}
</script>

{#if rows.length === 0}
	<p class="text-sm text-ink-muted">No audit events recorded.</p>
{:else}
	<!-- A rule between rows, not just space. An event is a sentence, a
	     reason and a field list, so two of them stacked with nothing between
	     them read as one paragraph that happened to change subject halfway
	     down — worst on the rows that carry detail, which is exactly where a
	     reader is counting fields. The left bar marks how far one row
	     extends; the rule says where the next begins. -->
	<ol class="divide-y divide-border-subtle" data-testid="audit-timeline">
		{#each rows as event (event.id)}
			<li
				class="border-l-2 border-border-subtle py-3 pl-3 first:pt-0 last:pb-0"
				data-testid="audit-event"
			>
				<!-- The sentence takes the line; the action name and the time
				     travel together as one muted group pinned to the right of
				     it. They used to be three siblings in one wrapping row
				     with the time pushed by ml-auto, so whether the time
				     landed beside the sentence or alone on the next line came
				     down to how long the sentence was — the list read as
				     ragged rather than as a column. Wrapping the pair keeps
				     them adjacent at any width: side by side when the row
				     fits, on their own line under the sentence when it does
				     not. -->
				<div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
					<p class="text-sm text-ink">
						<strong>{who(event)}</strong>
						{describe(event)}
						{#if onWhom(event)}
							<strong>{onWhom(event)}</strong>
						{/if}
					</p>
					<div class="flex items-baseline gap-2 font-mono text-meta text-ink-muted">
						<span>{event.action}</span>
						<time datetime={event.created_at}>{formatDateTime(event.created_at)}</time>
					</div>
				</div>

				{#if event.reason}
					<p class="mt-1 text-sm" data-testid="audit-reason">
						<span class="font-semibold text-ink-muted">Reason:</span>
						{event.reason}
					</p>
				{/if}

				<!-- A grid rather than a wrapping run of pairs: every name
				     lines up in its own column, so a long value wraps under
				     the value column instead of under whatever pair happened
				     to precede it, and the block reads as a field list. -->
				{#if details(event).length > 0}
					<dl
						class="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 font-mono text-meta text-ink-muted"
					>
						{#each details(event) as [key, value] (key)}
							<dt>{key}</dt>
							<!-- source_ip is the one field the feed filters on, so
							     on the feed it is a link to the view filtered to
							     it. Cobalt because it is a link (DESIGN.md, "Where
							     cobalt appears"); inline in the field list, so the
							     target rule's inline-link exemption applies. -->
							{#if key === 'source_ip' && sourceHref}
								<dd class="break-all">
									<a
										href={sourceHref(value)}
										onclick={(e) => followSource(e, value)}
										class="text-accent underline hover:text-accent-hover"
										data-testid="audit-source-link"
										>{value}<span class="sr-only">, show only events from this address</span></a
									>
								</dd>
							{:else}
								<dd class="break-all text-ink">{value}</dd>
							{/if}
						{/each}
					</dl>
				{/if}
			</li>
		{/each}
	</ol>
{/if}
