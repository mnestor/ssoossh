<script lang="ts">
	import type { PageMeta } from '$lib/api/types';

	import Icon from './Icon.svelte';

	// Offset pagination for the admin and auditor lists. The server sends the
	// window it served (server/webtypes.PageMeta) and this asks for another
	// one by offset, so the page arithmetic lives in exactly one place on each
	// side rather than being re-derived per list.
	//
	// Deliberately not built out of Button.svelte: the numbered controls need
	// aria-current and a per-page accessible name, which that primitive does
	// not carry. The classes below mirror its ghost variant so the two read as
	// one control.
	interface Props {
		/** The window the server just served. */
		meta: PageMeta;
		/** Asked for another window, by the offset it starts at. */
		onpage: (offset: number) => void;
		/** Disables every control while a page is in flight. */
		busy?: boolean;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { meta, onpage, busy = false, testid }: Props = $props();

	// How many numbered controls to render before eliding. Seven fits the
	// first page, the last page, the current page and its neighbours, and the
	// two ellipses, which is the widest a run can get once elision starts.
	const maxNumbered = 7;

	const isFirst = $derived(meta.page <= 1);
	const isLast = $derived(meta.page >= meta.page_count);

	// The numbered controls, with null standing for an elided run. Always
	// keeps the first and last page reachable: an auditor jumping to the end
	// of a long list should not have to page there.
	const pages = $derived.by((): (number | null)[] => {
		if (meta.page_count <= maxNumbered) {
			return Array.from({ length: meta.page_count }, (_, i) => i + 1);
		}

		// The first page, the last page, and the current page with a
		// neighbour either side. Sorted and de-duplicated rather than
		// collected in a set, which would be a mutable built-in inside a
		// derivation.
		const ordered = [1, meta.page - 1, meta.page, meta.page + 1, meta.page_count]
			.filter((value) => value >= 1 && value <= meta.page_count)
			.sort((a, b) => a - b)
			.filter((value, index, all) => index === 0 || value !== all[index - 1]);
		const withGaps: (number | null)[] = [];
		for (const [index, value] of ordered.entries()) {
			// A gap of exactly one page would render an ellipsis standing in
			// for a single control, which is wider than the control itself.
			if (index > 0 && value - ordered[index - 1] > 1) {
				withGaps.push(null);
			}
			withGaps.push(value);
		}
		return withGaps;
	});

	/** offsetOf returns the offset the 1-based page number starts at. */
	function offsetOf(page: number): number {
		return (page - 1) * meta.limit;
	}

	const controlClass =
		'inline-flex items-center justify-center gap-1.5 rounded-md border border-border-subtle px-3 py-1.5 text-xs font-semibold transition disabled:opacity-50';
</script>

{#if meta.page_count > 1}
	<nav
		aria-label="Pagination"
		data-testid={testid}
		class="flex flex-wrap items-center justify-between gap-3 border-t border-border-subtle pt-3"
	>
		<button
			type="button"
			class="{controlClass} text-ink hover:bg-surface-muted"
			disabled={isFirst || busy}
			onclick={() => onpage(Math.max(0, meta.offset - meta.limit))}
		>
			<Icon name="chevron-left" size="xs" />
			Previous
		</button>

		<div class="flex flex-wrap items-center gap-1">
			{#each pages as value, index (value === null ? `gap-${index}` : value)}
				{#if value === null}
					<span aria-hidden="true" class="px-1 text-xs text-ink-muted">…</span>
				{:else}
					<button
						type="button"
						aria-label="Page {value}"
						aria-current={value === meta.page ? 'page' : undefined}
						disabled={busy}
						onclick={() => onpage(offsetOf(value))}
						class="{controlClass} min-w-8 {value === meta.page
							? 'border-accent bg-accent text-accent-ink'
							: 'text-ink-muted hover:bg-surface-muted'}"
					>
						{value}
					</button>
				{/if}
			{/each}
		</div>

		<button
			type="button"
			class="{controlClass} text-ink hover:bg-surface-muted"
			disabled={isLast || busy}
			onclick={() => onpage(meta.offset + meta.limit)}
		>
			Next
			<Icon name="chevron-right" size="xs" />
		</button>

		<p class="w-full text-center text-xs text-ink-muted">
			Page {meta.page} of {meta.page_count}
		</p>
		<p class="w-full text-center text-xs text-ink-muted">
			{meta.total} total
		</p>
	</nav>
{/if}
