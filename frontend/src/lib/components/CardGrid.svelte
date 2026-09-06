<script lang="ts">
	import type { Snippet } from 'svelte';

	// A set of independent cards, two abreast once there is room.
	//
	// Every list of cards in the app was `flex flex-col gap-N`, so a page
	// given 1120px still stacked them single file and left the right half of
	// a wide monitor empty. This is for cards that genuinely do not depend on
	// each other — a config section, a diagnostic check — where reading order
	// across a row costs nothing. A sequence the reader is meant to work
	// through in order stays a column.
	//
	// The fold is at `xl` rather than `lg` because the rail takes 244px off
	// the front: at 1280px the page column is around 980px, which is where
	// two cards stop being cramped.
	interface Props {
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		children: Snippet;
	}

	let { testid, children }: Props = $props();
</script>

<div data-testid={testid} class="grid grid-cols-1 items-start gap-4 xl:grid-cols-2">
	{@render children()}
</div>
