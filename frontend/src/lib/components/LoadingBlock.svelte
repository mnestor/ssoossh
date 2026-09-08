<script lang="ts">
	// What a surface shows while its content is on the way.
	//
	// Fourteen screens each wrote their own `<p>Loading…</p>` — half of them
	// with the three-dot spelling, one centred, one not — which tells a
	// reader that a request is out and nothing about what is coming back.
	// A shape that matches the answer does both: the page stops jumping when
	// the rows land, and the wait is legible as "four rows" rather than as
	// one word.
	//
	// Nothing renders for the first 300ms. Below that a load is quicker than
	// a reader registers a change, and a placeholder that appears and
	// vanishes inside a third of a second reads as a flicker rather than as
	// progress.
	interface Props {
		/**
		 * Which shape the answer has:
		 * - `rows` for the card lists (certificates, service codes)
		 * - `table` for the admin tables
		 * - `lines` for a detail panel, where the answer is prose
		 */
		shape?: 'rows' | 'table' | 'lines';
		/** How many rows or lines to stand in for. */
		count?: number;
		/**
		 * An sr-only sentence announcing the wait, for a surface that has no
		 * live region of its own.
		 *
		 * Empty by default, and deliberately so: the lists carry `ListStatus`,
		 * which stays silent until the request settles precisely so a
		 * debounced search does not announce "Loading" once per keystroke.
		 * Announcing here as well would put back the noise that component
		 * exists to keep out. A detail page that loads once has no such
		 * problem and should pass a label.
		 */
		label?: string;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { shape = 'rows', count = 3, label = '', testid = 'loading-block' }: Props = $props();

	// The 300ms gate. `visible` starts false on every mount, so a surface
	// that re-requests (a filter chip, a page) re-arms it rather than
	// flashing a skeleton it already earned the right to skip.
	let visible = $state(false);

	$effect(() => {
		const timer = setTimeout(() => {
			visible = true;
		}, 300);
		return () => clearTimeout(timer);
	});

	// The widths make the placeholder read as text rather than as a bar
	// chart: a line of prose does not end in the same column twice.
	const lineWidths = ['w-full', 'w-11/12', 'w-9/12', 'w-10/12'];

	function lineWidth(index: number): string {
		return lineWidths[index % lineWidths.length];
	}
</script>

{#if label}
	<span role="status" class="sr-only">{label}</span>
{/if}

{#if visible}
	<!-- aria-hidden because none of this is content. What a reader needs to
	     know about the wait is in the live region above, or in the
	     `ListStatus` the page already carries. -->
	<div data-testid={testid} aria-hidden="true">
		{#if shape === 'rows'}
			<div class="flex flex-col gap-2.5">
				{#each { length: count }, i (i)}
					<div class="rounded-xl border border-border-subtle bg-surface px-5 py-3.5">
						<div class="flex items-center justify-between gap-4">
							<div class="flex min-w-0 flex-1 flex-col gap-2">
								<div class="skeleton h-3.5 w-2/5"></div>
								<div class="skeleton h-3 w-3/5"></div>
							</div>
							<div class="skeleton h-5 w-20 shrink-0 rounded-full"></div>
						</div>
					</div>
				{/each}
			</div>
		{:else if shape === 'table'}
			<div class="flex flex-col gap-3 py-2">
				{#each { length: count }, i (i)}
					<div class="flex items-center gap-4">
						<div class="skeleton h-3 w-1/4"></div>
						<div class="skeleton h-3 w-1/3"></div>
						<div class="skeleton h-3 w-1/6"></div>
					</div>
				{/each}
			</div>
		{:else}
			<div class="flex flex-col gap-2.5">
				{#each { length: count }, i (i)}
					<div class="skeleton h-3.5 {lineWidth(i)}"></div>
				{/each}
			</div>
		{/if}
	</div>
{/if}
