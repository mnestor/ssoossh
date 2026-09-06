<script lang="ts" generics="T">
	import { splitIntoColumns } from '$lib/columns';
	import type { Snippet } from 'svelte';

	// A set of independent cards, two abreast once there is room.
	//
	// Every list of cards in the app was `flex flex-col gap-N`, so a page
	// given 1120px still stacked them single file and left the right half of
	// a wide monitor empty. This is for cards that do not depend on each
	// other — a config section, a diagnostic check. A sequence the reader is
	// meant to work through in order stays a column.
	//
	// Two layouts were tried and rejected before this one, and both failures
	// are worth keeping written down:
	//
	//   A two-column grid. A grid row is as tall as the tallest card in it,
	//   so a three-key config section beside a fifty-two-key one left a hole
	//   under the short card until the next row began. The page read as
	//   gappier than the single column it replaced.
	//
	//   CSS multi-column. It packs vertically, but the balancing is the
	//   browser's to do and not ours to see: the effective-configuration
	//   screen is fifteen sections ranging from one setting to fifty-two,
	//   and how that lands is a thing you find out by looking rather than a
	//   thing you can state.
	//
	// So the columns are assigned here. Flexbox cannot do this on its own —
	// wrapping a column direction needs a fixed height, which nothing here
	// has — but once the split is decided, flex columns render it exactly:
	// each column packs its own cards against one another, and no card's
	// height reaches across into the other column.
	interface Props {
		items: T[];
		/** Rendered once per item. */
		card: Snippet<[T]>;
		/**
		 * Roughly how tall an item will be, in whatever unit is convenient —
		 * rows of content, findings, list entries. Only the ratios matter.
		 * Omit it and every card counts the same, which balances by number
		 * of cards.
		 */
		weight?: (item: T) => number;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { items, card, weight, testid }: Props = $props();

	// Every card costs a header, a border and its padding before it holds
	// anything, so a column of many small cards is not the empty space its
	// content weight alone would suggest. Three content rows is about what
	// that furniture measures.
	const cardOverhead = 3;

	const columns = $derived(
		splitIntoColumns(
			items.map((item) => cardOverhead + (weight ? weight(item) : 0)),
			2
		)
	);
</script>

<!-- Below `xl` this is one column, and because the runs are contiguous the
     cards come out in the order they were given. The fold is at `xl` rather
     than `lg` because the rail takes 244px off the front: at 1280px the page
     column is around 980px, which is where two cards stop being cramped. -->
<div data-testid={testid} class="flex flex-col gap-4 xl:flex-row xl:items-start">
	{#each columns as column, position (position)}
		<!-- An empty column still claims half the width if it is rendered,
		     which is how a single card ends up looking like a half-empty
		     page. -->
		{#if column.length > 0}
			<div class="flex min-w-0 flex-col gap-4 xl:flex-1">
				{#each column as index (index)}
					{@render card(items[index])}
				{/each}
			</div>
		{/if}
	{/each}
</div>
